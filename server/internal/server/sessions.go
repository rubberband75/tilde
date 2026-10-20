package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/hashicorp/yamux"

	"tilde/internal/mux"
	"tilde/internal/protocol"
	"tilde/internal/stdio"
)

// AuthError is a login failure the user should see.
type AuthError struct {
	Code    string
	Message string
}

func (e *AuthError) Error() string { return e.Message }

// UserSession is one user's running desktop session: a root session helper
// holding the PAM session open, and the worker it started as the user.
// All browser tabs and devices of that user share it (decision 4a).
type UserSession struct {
	User    string
	UID     int
	Home    string
	Started time.Time

	cmd   *exec.Cmd
	conn  *yamux.Session
	proxy http.Handler
	done  chan struct{}
}

// Done is closed when the session has ended.
func (us *UserSession) Done() <-chan struct{} { return us.done }

// ServeHTTP forwards a browser request to the user's worker.
func (us *UserSession) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	us.proxy.ServeHTTP(w, r)
}

// Manager starts, tracks and ends user sessions.
type Manager struct {
	self  string // path of the tilde binary
	onEnd func(user string)

	mu       sync.Mutex
	sessions map[string]*UserSession
	locks    map[string]*sync.Mutex
}

// NewManager returns a Manager that runs helpers from the binary at self.
// onEnd is called after a session ends for any reason.
func NewManager(self string, onEnd func(user string)) *Manager {
	return &Manager{
		self:     self,
		onEnd:    onEnd,
		sessions: map[string]*UserSession{},
		locks:    map[string]*sync.Mutex{},
	}
}

// Get returns the user's running session, or nil.
func (m *Manager) Get(user string) *UserSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[user]
}

func (m *Manager) userLock(user string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.locks[user]
	if !ok {
		l = &sync.Mutex{}
		m.locks[user] = l
	}
	return l
}

// Login checks the password and returns the user's session, starting one if
// none is running. Every login goes through PAM, even when a session exists.
func (m *Manager) Login(ctx context.Context, user, password, remoteAddr string) (*UserSession, error) {
	l := m.userLock(user)
	l.Lock()
	defer l.Unlock()

	req := protocol.HelperRequest{User: user, Password: password, RemoteAddr: remoteAddr}
	if us := m.Get(user); us != nil {
		req.Mode = protocol.ModeVerify
		h, res, err := m.spawn(ctx, req)
		if err != nil {
			return nil, err
		}
		h.cmd.Wait()
		if !res.OK {
			return nil, &AuthError{Code: res.Code, Message: res.Error}
		}
		return us, nil
	}
	req.Mode = protocol.ModeSession
	return m.startSession(ctx, req)
}

// Verify checks a password without touching the session (for unlocking).
func (m *Manager) Verify(ctx context.Context, user, password, remoteAddr string) error {
	h, res, err := m.spawn(ctx, protocol.HelperRequest{
		Mode: protocol.ModeVerify, User: user, Password: password, RemoteAddr: remoteAddr,
	})
	if err != nil {
		return err
	}
	h.cmd.Wait()
	if !res.OK {
		return &AuthError{Code: res.Code, Message: res.Error}
	}
	return nil
}

type helperProc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	br     *bufio.Reader
}

// spawn starts "tilde session-helper", sends the request and reads the
// helper's result line.
func (m *Manager) spawn(ctx context.Context, req protocol.HelperRequest) (*helperProc, protocol.HelperResult, error) {
	var res protocol.HelperResult
	cmd := exec.Command(m.self, "session-helper")
	// The helper gets a minimal, known environment, never the server's.
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8"}
	if v := os.Getenv("XDG_RUNTIME_DIR"); v != "" && os.Geteuid() != 0 {
		cmd.Env = append(cmd.Env, "XDG_RUNTIME_DIR="+v) // single-user dev mode
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, res, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, res, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, res, err
	}
	if err := cmd.Start(); err != nil {
		return nil, res, fmt.Errorf("start session helper: %w", err)
	}
	go logLines(stderr, req.User)

	h := &helperProc{cmd: cmd, stdin: stdin, stdout: stdout, br: bufio.NewReaderSize(stdout, 64*1024)}
	if err := protocol.WriteLine(stdin, req); err != nil {
		h.kill()
		return nil, res, fmt.Errorf("send request to helper: %w", err)
	}
	// PAM may sleep after a failure (pam_faildelay), so allow plenty of time.
	if err := readLineTimeout(ctx, h, &res, 60*time.Second); err != nil {
		return nil, res, fmt.Errorf("read helper result: %w", err)
	}
	return h, res, nil
}

func (h *helperProc) kill() {
	_ = h.cmd.Process.Kill()
	_ = h.cmd.Wait()
}

func readLineTimeout(ctx context.Context, h *helperProc, v any, timeout time.Duration) error {
	errc := make(chan error, 1)
	go func() { errc <- protocol.ReadLine(h.br, v) }()
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case err := <-errc:
		if err != nil {
			h.kill()
		}
		return err
	case <-t.C:
		h.kill()
		return errors.New("timed out")
	case <-ctx.Done():
		h.kill()
		return ctx.Err()
	}
}

func (m *Manager) startSession(ctx context.Context, req protocol.HelperRequest) (*UserSession, error) {
	h, res, err := m.spawn(ctx, req)
	if err != nil {
		return nil, err
	}
	if !res.OK {
		h.cmd.Wait()
		return nil, &AuthError{Code: res.Code, Message: res.Error}
	}
	var hello protocol.WorkerHello
	if err := readLineTimeout(ctx, h, &hello, 20*time.Second); err != nil {
		return nil, fmt.Errorf("worker did not start: %w", err)
	}
	if hello.Protocol != protocol.Version {
		h.kill()
		return nil, fmt.Errorf("worker speaks protocol %d, server speaks %d", hello.Protocol, protocol.Version)
	}

	conn := &stdio.Conn{Reader: h.br, Writer: h.stdin, Closers: []io.Closer{h.stdin, h.stdout}}
	ys, err := yamux.Client(conn, mux.Config())
	if err != nil {
		h.kill()
		return nil, err
	}
	us := &UserSession{
		User:    hello.User,
		UID:     hello.UID,
		Home:    hello.Home,
		Started: time.Now(),
		cmd:     h.cmd,
		conn:    ys,
		proxy:   newWorkerProxy(ys),
		done:    make(chan struct{}),
	}
	m.mu.Lock()
	m.sessions[req.User] = us
	m.mu.Unlock()
	slog.Info("session started", "user", us.User, "uid", us.UID)

	// If the stream breaks (keepalive failure), stop the helper.
	go func() {
		select {
		case <-ys.CloseChan():
			_ = h.cmd.Process.Signal(syscall.SIGTERM)
		case <-us.done:
		}
	}()
	go func() {
		err := h.cmd.Wait()
		ys.Close()
		m.mu.Lock()
		if m.sessions[req.User] == us {
			delete(m.sessions, req.User)
		}
		m.mu.Unlock()
		close(us.done)
		slog.Info("session ended", "user", us.User, "err", err)
		if m.onEnd != nil {
			m.onEnd(req.User)
		}
	}()
	return us, nil
}

// End logs the user out: the worker's pipe is closed, the worker hangs up
// its terminals and exits, and the helper closes the PAM session.
func (m *Manager) End(user string) {
	us := m.Get(user)
	if us == nil {
		return
	}
	us.conn.Close()
	select {
	case <-us.done:
		return
	case <-time.After(5 * time.Second):
	}
	_ = us.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-us.done:
	case <-time.After(5 * time.Second):
		_ = us.cmd.Process.Kill()
		<-us.done
	}
}

// EndAll ends every session (server shutdown).
func (m *Manager) EndAll() {
	m.mu.Lock()
	users := make([]string, 0, len(m.sessions))
	for u := range m.sessions {
		users = append(users, u)
	}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, u := range users {
		wg.Add(1)
		go func() { defer wg.Done(); m.End(u) }()
	}
	wg.Wait()
}

// newWorkerProxy forwards requests over new yamux streams. WebSocket
// upgrades pass through, which is how terminals reach the worker's PTYs.
func newWorkerProxy(ys *yamux.Session) http.Handler {
	target := &url.URL{Scheme: "http", Host: "worker"}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return ys.Open()
		},
		MaxIdleConnsPerHost: 32,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true,
	}
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = "worker"
			// The worker trusts the server; it never needs the browser's
			// credentials.
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("Authorization")
		},
		Transport:     transport,
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Warn("proxy to worker failed", "path", r.URL.Path, "err", err)
			writeJSON(w, http.StatusBadGateway, apiError{Error: "the session is not responding", Code: "worker_unavailable"})
		},
	}
}

func logLines(r io.Reader, user string) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		slog.Info("session log", "user", user, "line", sc.Text())
	}
}
