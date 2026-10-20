// Package pty runs shells on pseudo-terminals that outlive browser
// connections. Each Terminal keeps recent output in a ring buffer so a
// reattaching client can be brought up to date.
package pty

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	cpty "github.com/creack/pty"

	"tilde/internal/ringbuf"
)

// DefaultRingSize is the replay buffer per terminal (D14).
const DefaultRingSize = 1 << 20

// subscriberBuffer is how many output chunks a slow client may fall behind
// before it is disconnected (it can reattach and get a replay).
const subscriberBuffer = 512

// Options configures a new terminal.
type Options struct {
	Shell    string
	Login    bool // start as a login shell ("-bash")
	Dir      string
	Env      []string
	Cols     uint16
	Rows     uint16
	RingSize int
}

// Terminal is one running shell.
type Terminal struct {
	ID      string
	Created time.Time

	f    *os.File
	cmd  *exec.Cmd
	done chan struct{}

	mu       sync.Mutex
	ring     *ringbuf.Buffer
	subs     map[chan []byte]struct{}
	cols     uint16
	rows     uint16
	exited   bool
	exitCode int
}

// Start launches a shell on a new PTY.
func Start(id string, opt Options) (*Terminal, error) {
	if opt.Shell == "" {
		return nil, errors.New("pty: no shell")
	}
	if opt.Cols == 0 {
		opt.Cols = 80
	}
	if opt.Rows == 0 {
		opt.Rows = 24
	}
	if opt.RingSize <= 0 {
		opt.RingSize = DefaultRingSize
	}
	cmd := exec.Command(opt.Shell)
	if opt.Login {
		// Same convention login(1) uses: argv[0] starting with "-".
		cmd.Args = []string{"-" + filepath.Base(opt.Shell)}
	}
	cmd.Dir = opt.Dir
	cmd.Env = opt.Env
	f, err := cpty.StartWithSize(cmd, &cpty.Winsize{Cols: opt.Cols, Rows: opt.Rows})
	if err != nil {
		return nil, err
	}
	t := &Terminal{
		ID:      id,
		Created: time.Now(),
		f:       f,
		cmd:     cmd,
		done:    make(chan struct{}),
		ring:    ringbuf.New(opt.RingSize),
		subs:    make(map[chan []byte]struct{}),
		cols:    opt.Cols,
		rows:    opt.Rows,
	}
	go t.readLoop()
	go t.waitLoop()
	return t, nil
}

func (t *Terminal) readLoop() {
	buf := make([]byte, 32*1024)
	for {
		n, err := t.f.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			t.mu.Lock()
			t.ring.Write(chunk)
			for ch := range t.subs {
				select {
				case ch <- chunk:
				default:
					// Too slow: drop this client rather than block the shell.
					delete(t.subs, ch)
					close(ch)
				}
			}
			t.mu.Unlock()
		}
		if err != nil {
			break
		}
	}
	t.mu.Lock()
	t.exited = true
	for ch := range t.subs {
		delete(t.subs, ch)
		close(ch)
	}
	t.mu.Unlock()
	close(t.done)
}

func (t *Terminal) waitLoop() {
	err := t.cmd.Wait()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	}
	t.mu.Lock()
	t.exitCode = code
	t.mu.Unlock()
	// The shell is gone. Give readLoop a moment to drain its last output,
	// then close the master so readLoop ends even if a background job still
	// holds the slave side open.
	select {
	case <-t.done:
	case <-time.After(200 * time.Millisecond):
		t.f.Close()
	}
}

// Attach returns the buffered output so far and a channel of new output.
// The snapshot and the subscription are taken under one lock, so nothing is
// lost or repeated between them. The channel is closed when the terminal
// exits or the client falls too far behind. Call detach when done.
func (t *Terminal) Attach() (replay []byte, out <-chan []byte, detach func()) {
	ch := make(chan []byte, subscriberBuffer)
	t.mu.Lock()
	replay = t.ring.Bytes()
	if t.exited {
		close(ch)
	} else {
		t.subs[ch] = struct{}{}
	}
	t.mu.Unlock()
	detach = func() {
		t.mu.Lock()
		if _, ok := t.subs[ch]; ok {
			delete(t.subs, ch)
			close(ch)
		}
		t.mu.Unlock()
	}
	return replay, ch, detach
}

// Write sends input to the shell.
func (t *Terminal) Write(p []byte) (int, error) { return t.f.Write(p) }

// Resize sets the window size. With force, it makes sure the shell receives
// SIGWINCH even if the size did not change, so full-screen programs (vim,
// htop) redraw after a reattach. The kernel only signals on a real change,
// so force briefly nudges the width by one column.
func (t *Terminal) Resize(cols, rows uint16, force bool) error {
	if cols == 0 || rows == 0 {
		return nil
	}
	t.mu.Lock()
	same := cols == t.cols && rows == t.rows
	t.cols, t.rows = cols, rows
	t.mu.Unlock()
	if same && force && cols > 1 {
		if err := cpty.Setsize(t.f, &cpty.Winsize{Cols: cols - 1, Rows: rows}); err != nil {
			return err
		}
	} else if same {
		return nil
	}
	return cpty.Setsize(t.f, &cpty.Winsize{Cols: cols, Rows: rows})
}

// Size returns the current window size.
func (t *Terminal) Size() (cols, rows uint16) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cols, t.rows
}

// Exited reports whether the shell has exited, and its exit code.
func (t *Terminal) Exited() (bool, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.exited, t.exitCode
}

// Done is closed once the terminal has fully shut down.
func (t *Terminal) Done() <-chan struct{} { return t.done }

// Close hangs up the shell (SIGHUP, like closing a terminal window) and
// kills it if it has not exited within two seconds.
func (t *Terminal) Close() {
	if t.cmd.Process != nil {
		_ = t.cmd.Process.Signal(syscall.SIGHUP)
	}
	select {
	case <-t.done:
		return
	case <-time.After(2 * time.Second):
	}
	if t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
	t.f.Close()
	<-t.done
}
