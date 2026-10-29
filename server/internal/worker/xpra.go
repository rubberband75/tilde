package worker

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Host apps through Xpra (D26, D71, D78): the worker starts the user's
// Xpra session when the desktop asks (never because something connected),
// bound only to a socket in the user's runtime folder (no TCP port: rule
// 5), and stops it when the session ends, as logging out of a desktop ends
// its apps. Xpra's HTML5 client is served by Xpra itself over that socket,
// and the browser reaches it through Tilde's Xpra port, an origin of its
// own (D78), which the worker connects to the socket.
//
// Xpra and the apps the worker starts on its display run in one systemd
// user slice, so stopping it ends them all, Xvfb and helpers included,
// which do not stay in Xpra's process group; a worker that crashed leaves
// it behind, and the next start clears it first.

// xpraWWW is where xpra-html5 installs its client; without it Xpra
// cannot serve the browser.
var xpraWWW = []string{"/usr/share/xpra/www/index.html"}

// The commands run; tests replace them.
var (
	xpraCommand = "xpra"
	systemdRun  = "systemd-run"
	systemctl   = "systemctl"
)

// xpraSlice holds the session and its apps.
const xpraSlice = "tilde-xpra.slice"

var (
	errNoXpra         = errors.New("Xpra is not installed (xpra and xpra-html5)")
	errXpraNotRunning = errors.New("the host apps session is not running")
	errXpraClosed     = errors.New("the session is ending")
)

type xpraSession struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	sock    string
	display string
	done    chan struct{}
	// closed: the worker is shutting down; nothing starts again.
	closed bool
	// slice: Xpra and apps run in xpraSlice (systemd-run --user works).
	slice bool
	// failed is when a start last failed, and why, so a desktop asking
	// again and again does not start an Xpra each time.
	failed    time.Time
	failedErr error
	// launching bounds the apps being started at once.
	launching chan struct{}
}

// xpraRetry is how long a failed start is not tried again.
const xpraRetry = 30 * time.Second

func newXpraSession() *xpraSession {
	return &xpraSession{launching: make(chan struct{}, 2)}
}

// available reports whether Xpra and its HTML5 client are installed, and
// there is a runtime folder for the socket.
func (x *xpraSession) available() bool {
	if _, err := exec.LookPath(xpraCommand); err != nil || os.Getenv("XDG_RUNTIME_DIR") == "" {
		return false
	}
	for _, f := range xpraWWW {
		if _, err := os.Stat(f); err == nil {
			return true
		}
	}
	return false
}

func (x *xpraSession) socketPath() string {
	return filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "tilde", "xpra.sock")
}

// running reports whether the session Tilde started is up: alive, and
// listening (Xpra makes its socket when it does). It does not connect to
// find out: Xpra holds a connection that says nothing open for a while,
// and a few of those fill its queue. x.mu must be held.
func (x *xpraSession) running() bool {
	if x.cmd == nil || x.exited() {
		return false
	}
	st, err := os.Stat(x.sock)
	return err == nil && st.Mode()&os.ModeSocket != 0
}

func (x *xpraSession) exited() bool {
	select {
	case <-x.done:
		return true
	default:
		return false
	}
}

// ensure starts the session if it is not running, and waits for its
// socket. One Xpra at a time: while one is starting, callers wait for it.
func (x *xpraSession) ensure(ctx context.Context) (string, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.closed {
		return "", errXpraClosed
	}
	if !x.available() {
		return "", errNoXpra
	}
	if x.running() {
		return x.sock, nil
	}
	if x.cmd == nil || x.exited() {
		x.cmd = nil
		if time.Since(x.failed) < xpraRetry {
			return "", x.failedErr
		}
		if err := x.start(); err != nil {
			return "", x.fail(err)
		}
	}
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	wait := time.NewTimer(45 * time.Second)
	defer wait.Stop()
	for {
		select {
		case <-x.done:
			return "", x.fail(fmt.Errorf("Xpra exited while starting; see %s", filepath.Join(filepath.Dir(x.sock), "xpra.log")))
		case <-wait.C:
			// Still starting (a busy machine): the next caller waits on.
			return "", errors.New("Xpra is still starting; try again in a moment")
		case <-ctx.Done():
			return "", ctx.Err()
		case <-tick.C:
			if x.running() {
				return x.sock, nil
			}
		}
	}
}

func (x *xpraSession) fail(err error) error {
	x.failed, x.failedErr = time.Now(), err
	slog.Warn("worker: Xpra", "err", err)
	return err
}

// sliceWorks reports whether systemd-run --user can put Xpra in a slice
// (a user manager on the session bus).
func sliceWorks() bool {
	if _, err := exec.LookPath(systemdRun); err != nil {
		return false
	}
	return exec.Command(systemctl, "--user", "show-environment").Run() == nil
}

// inSlice wraps argv to run in xpraSlice, when that works.
func (x *xpraSession) inSlice(argv []string) []string {
	if !x.slice {
		return argv
	}
	return append([]string{systemdRun, "--user", "--scope", "--quiet", "--collect", "--slice=" + xpraSlice, "--"}, argv...)
}

// start runs Xpra, bound to its socket only, in the slice. x.mu must be
// held.
func (x *xpraSession) start() error {
	x.slice = sliceWorks()
	if x.slice {
		// What a worker that crashed left: an old session and its apps.
		exec.Command(systemctl, "--user", "stop", xpraSlice).Run()
	}
	sock := x.socketPath()
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		return err
	}
	os.Remove(sock) // from that old session, now stopped
	// Its output goes to its log, never the worker's stdout (rule 4).
	logFile, err := os.OpenFile(filepath.Join(filepath.Dir(sock), "xpra.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	// Xpra writes its display number to this pipe once it has one.
	displayR, displayW, err := os.Pipe()
	if err != nil {
		return err
	}
	defer displayR.Close()
	argv := x.inSlice([]string{xpraCommand, "start",
		// Only the user: peer credentials on a socket in a 0700 folder.
		"--bind=" + sock + ",auth=peercred", "--html=on", "--daemon=no", "--displayfd=3",
		// The user's session bus, so apps' notifications come to Tilde (D70).
		"--dbus-launch=no", "--mdns=no", "--notifications=no", "--pulseaudio=no",
		// Nothing a page could ask of the server besides showing windows:
		// no commands (the worker starts apps), files, URLs, printing,
		// webcam, server scripts, or a system proxy.
		"--start-new-commands=no", "--file-transfer=no", "--open-files=no", "--open-url=no",
		"--printing=no", "--webcam=no", "--http-scripts=off", "--start-via-proxy=no",
		"--systemd-run=no", "--exit-with-children=no", "--terminate-children=yes"})
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = os.Getenv("HOME")
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.ExtraFiles = []*os.File{displayW}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		displayW.Close()
		return fmt.Errorf("start Xpra: %w", err)
	}
	displayW.Close()
	done := make(chan struct{})
	go func() {
		cmd.Wait()
		// What is left of its group, at once: later the number may be
		// another group's.
		syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		close(done)
	}()
	x.cmd, x.sock, x.done, x.display = cmd, sock, done, ""
	displayR.SetReadDeadline(time.Now().Add(45 * time.Second))
	if line, err := bufio.NewReader(displayR).ReadString('\n'); err == nil || line != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(line)); err == nil {
			x.display = ":" + strconv.Itoa(n)
		}
	}
	slog.Info("worker: starting Xpra", "socket", sock, "display", x.display, "slice", x.slice)
	return nil
}

// launch starts an app on the session's display, with its argv as is (no
// shell), in the slice, starting the session first if need be.
func (x *xpraSession) launch(ctx context.Context, argv []string) error {
	select {
	case x.launching <- struct{}{}:
		defer func() { <-x.launching }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if _, err := x.ensure(ctx); err != nil {
		return err
	}
	x.mu.Lock()
	display, full := x.display, x.inSlice(argv)
	x.mu.Unlock()
	if display == "" {
		return errors.New("Xpra did not say which display it runs")
	}
	logFile, err := os.OpenFile(filepath.Join(filepath.Dir(x.socketPath()), "apps.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	cmd := exec.Command(full[0], full[1:]...)
	cmd.Dir = os.Getenv("HOME")
	cmd.Env = append(os.Environ(), "DISPLAY="+display, "XDG_SESSION_TYPE=x11", "GDK_BACKEND=x11", "QT_QPA_PLATFORM=xcb")
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", argv[0], err)
	}
	go cmd.Wait()
	return nil
}

// dial connects to the session's socket. It does not start the session:
// only the desktop does (the launch and session endpoints, on Tilde's
// own origin). A full queue (EAGAIN: the browser's client opens many
// connections at once), and a socket made but not listening yet
// (ECONNREFUSED, as Xpra starts), are waited out briefly.
func (x *xpraSession) dial(ctx context.Context) (net.Conn, error) {
	x.mu.Lock()
	up, sock := x.running(), x.sock
	x.mu.Unlock()
	if !up {
		return nil, errXpraNotRunning
	}
	var d net.Dialer
	for i := 0; ; i++ {
		c, err := d.DialContext(ctx, "unix", sock)
		if err == nil || !(errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.ECONNREFUSED)) || i == 30 {
			return c, err
		}
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// stop ends the session and its apps, for good: the worker is shutting
// down.
func (x *xpraSession) stop() {
	x.mu.Lock()
	x.closed = true
	cmd, done, sock, slice := x.cmd, x.done, x.sock, x.slice
	x.cmd = nil
	x.mu.Unlock()
	if cmd == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	exec.CommandContext(ctx, xpraCommand, "stop", "socket://"+sock).Run()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	if slice {
		// The apps, Xvfb and helpers, whatever their process groups.
		exec.Command(systemctl, "--user", "stop", xpraSlice).Run()
	}
}
