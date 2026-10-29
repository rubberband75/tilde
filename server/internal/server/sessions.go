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
	"strconv"
	"sync"
	"sync/atomic"
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
// All browser tabs and devices of that user share it (D12).
type UserSession struct {
	User    string
	UID     int
	Home    string
	Started time.Time

	cmd   *exec.Cmd
	conn  *yamux.Session
	proxy http.Handler
	// client makes the server's own requests to the worker (/internal/).
	client *http.Client
	done   chan struct{}

	// lockAfter is the user's idle lock delay, nil until the worker has
	// reported it (D40).
	lockAfter atomic.Pointer[time.Duration]
	// lockRequested locks every browser of the user when logind asks the
	// session to lock; allLocked says whether every browser is locked, for
	// logind's LockedHint; hint wakes followLockedHint (D41).
	lockRequested func()
	allLocked     func() bool
	hint          chan struct{}
	// audit writes the worker's audit events under this user (D46).
	audit *auditLog
	// apps is the connection pool to the user's local web apps (D76).
	appsMu sync.Mutex
	apps   *http.Transport
	xpra   *http.Transport
}

// Done is closed when the session has ended.
func (us *UserSession) Done() <-chan struct{} { return us.done }

// defaultLockAfter applies until the worker reports the user's settings:
// Cinnamon's default.
const defaultLockAfter = 15 * time.Minute

// LockAfter returns how long the user's clients may go without input
// before they lock; 0 never locks.
func (us *UserSession) LockAfter() time.Duration {
	if d := us.lockAfter.Load(); d != nil {
		return *d
	}
	return defaultLockAfter
}

// ServeHTTP forwards a browser request to the user's worker.
func (us *UserSession) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	us.proxy.ServeHTTP(w, r)
}

// Manager starts, tracks and ends user sessions.
type Manager struct {
	self  string // path of the tilde binary
	onEnd func(user string)
	// lockRequested and allLocked connect sessions to the token store
	// (D41): logind's lock requests, and its LockedHint.
	lockRequested func(user string)
	allLocked     func(user string) bool
	// audit receives sessions starting and ending, and what the worker
	// reports (D46).
	audit *auditLog

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

// Auth is a password check through a session helper. It can take more
// than one step: PAM may ask more than the password, such as a
// verification code (D43). While Prompt returns a question the helper
// waits, and Answer goes on; otherwise the check passed, and for a login
// Session is the user's session. Failures come back as *AuthError.
type Auth struct {
	prompt  *protocol.Prompt
	session *UserSession

	m   *Manager
	h   *helperProc
	req protocol.HelperRequest
	uid int
}

// Login checks the password and returns the user's session, starting one if
// none is running. Every login goes through PAM, even when a session exists.
func (m *Manager) Login(ctx context.Context, user, password, remoteAddr string) (*Auth, error) {
	return m.begin(ctx, protocol.HelperRequest{Mode: protocol.ModeSession, User: user, Password: password, RemoteAddr: remoteAddr})
}

// Verify checks a password without touching the session (for unlocking).
func (m *Manager) Verify(ctx context.Context, user, password, remoteAddr string) (*Auth, error) {
	return m.begin(ctx, protocol.HelperRequest{Mode: protocol.ModeVerify, User: user, Password: password, RemoteAddr: remoteAddr})
}

func (m *Manager) begin(ctx context.Context, req protocol.HelperRequest) (*Auth, error) {
	h, err := m.spawn(req)
	req.Password = ""
	if err != nil {
		return nil, err
	}
	a := &Auth{m: m, h: h, req: req}
	return a, a.next(ctx)
}

// Prompt is PAM's question waiting for an answer, or nil.
func (a *Auth) Prompt() *protocol.Prompt { return a.prompt }

// Session is the user's session once a login has passed.
func (a *Auth) Session() *UserSession { return a.session }

// Answer gives PAM the user's answer to Prompt and goes on.
func (a *Auth) Answer(ctx context.Context, answer string) error {
	if a.prompt == nil {
		return errors.New("no question is waiting for an answer")
	}
	a.prompt = nil
	if err := protocol.WriteLine(a.h.stdin, protocol.HelperAnswer{Answer: answer}); err != nil {
		a.h.kill()
		return fmt.Errorf("send answer to helper: %w", err)
	}
	return a.next(ctx)
}

// Cancel stops a check that is waiting for an answer.
func (a *Auth) Cancel() {
	if a.prompt != nil {
		a.prompt = nil
		a.h.kill()
	}
}

// stepTimeout is how long PAM may take over one step: it sleeps after a
// failure (pam_faildelay), and push-style modules wait for a phone.
var stepTimeout = 2 * time.Minute

// next reads the helper's next step: a question, or the result. Callers
// pass a context the browser cannot cancel: a helper killed mid-check
// would leave no verdict, and a disconnect must not undo a wrong guess.
func (a *Auth) next(ctx context.Context) error {
	var res protocol.HelperResult
	if err := readLineTimeout(ctx, a.h, &res, stepTimeout); err != nil {
		return fmt.Errorf("read helper result: %w", err)
	}
	if res.Prompt != nil {
		a.prompt = res.Prompt
		return nil
	}
	if !res.OK {
		a.h.cmd.Wait()
		return &AuthError{Code: res.Code, Message: res.Error}
	}
	a.uid = res.UID
	if a.req.Mode == protocol.ModeVerify {
		a.h.cmd.Wait()
		return nil
	}
	us, err := a.m.join(ctx, a)
	a.session = us
	return err
}

// join decides, once the user is authenticated, whether this login starts
// the session or joins the one that is running, which may have started
// while this login waited for a code. The user's lock keeps two logins from
// starting two sessions.
func (m *Manager) join(ctx context.Context, a *Auth) (*UserSession, error) {
	l := m.userLock(a.req.User)
	l.Lock()
	defer l.Unlock()
	if us := m.Get(a.req.User); us != nil {
		_ = protocol.WriteLine(a.h.stdin, protocol.HelperStart{Start: false})
		a.h.cmd.Wait()
		return us, nil
	}
	if err := protocol.WriteLine(a.h.stdin, protocol.HelperStart{Start: true}); err != nil {
		a.h.kill()
		return nil, fmt.Errorf("tell helper to start: %w", err)
	}
	var res protocol.HelperResult
	if err := readLineTimeout(ctx, a.h, &res, 60*time.Second); err != nil {
		return nil, fmt.Errorf("read helper result: %w", err)
	}
	if !res.OK {
		a.h.cmd.Wait()
		return nil, &AuthError{Code: res.Code, Message: res.Error}
	}
	return m.startSession(ctx, a.h, a.req, a.uid)
}

type helperProc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	br     *bufio.Reader
}

// spawn starts "tilde session-helper" and sends it the request.
func (m *Manager) spawn(req protocol.HelperRequest) (*helperProc, error) {
	cmd := exec.Command(m.self, "session-helper")
	// The helper gets a minimal, known environment, never the server's.
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8"}
	if v := os.Getenv("XDG_RUNTIME_DIR"); v != "" && os.Geteuid() != 0 {
		cmd.Env = append(cmd.Env, "XDG_RUNTIME_DIR="+v) // single-user dev mode
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start session helper: %w", err)
	}
	go logLines(stderr, req.User)

	h := &helperProc{cmd: cmd, stdin: stdin, stdout: stdout, br: bufio.NewReaderSize(stdout, 64*1024)}
	if err := protocol.WriteLine(stdin, req); err != nil {
		h.kill()
		return nil, fmt.Errorf("send request to helper: %w", err)
	}
	return h, nil
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

// startSession takes over the worker a helper has just started, once the
// session is open. m's lock for the user is held.
func (m *Manager) startSession(ctx context.Context, h *helperProc, req protocol.HelperRequest, uid int) (*UserSession, error) {
	var hello protocol.WorkerHello
	if err := readLineTimeout(ctx, h, &hello, 20*time.Second); err != nil {
		return nil, fmt.Errorf("worker did not start: %w", err)
	}
	if hello.Protocol != protocol.Version {
		h.kill()
		return nil, fmt.Errorf("worker speaks protocol %d, server speaks %d", hello.Protocol, protocol.Version)
	}
	// The hello comes from a process the user owns, the helper's result
	// from root. Tokens are issued for the hello's user, so it must be the
	// account that logged in, at the uid the helper started.
	if hello.User != req.User || hello.UID != uid {
		h.kill()
		return nil, fmt.Errorf("worker says it runs as %q (uid %d), but %q (uid %d) logged in", hello.User, hello.UID, req.User, uid)
	}

	conn := &stdio.Conn{Reader: h.br, Writer: h.stdin, Closers: []io.Closer{h.stdin, h.stdout}}
	ys, err := yamux.Client(conn, mux.Config())
	if err != nil {
		h.kill()
		return nil, err
	}
	transport := newWorkerTransport(ys)
	us := &UserSession{
		User:    hello.User,
		UID:     hello.UID,
		Home:    hello.Home,
		Started: time.Now(),
		cmd:     h.cmd,
		conn:    ys,
		proxy:   newWorkerProxy(transport),
		client:  &http.Client{Transport: transport},
		done:    make(chan struct{}),
		hint:    make(chan struct{}, 1),
		lockRequested: func() {
			if m.lockRequested != nil {
				m.lockRequested(req.User)
			}
		},
		allLocked: func() bool { return m.allLocked != nil && m.allLocked(req.User) },
		audit:     m.audit,
	}
	m.mu.Lock()
	m.sessions[req.User] = us
	m.mu.Unlock()
	slog.Info("session started", "user", us.User, "uid", us.UID)
	m.audit.record(auditRecord{Event: "session_start", User: us.User, Text: "started a session", Fields: [][2]string{{"UID", strconv.Itoa(us.UID)}}})
	go us.followSession()
	go us.followLockedHint()

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
		m.audit.record(auditRecord{Event: "session_end", User: us.User, Text: "ended a session"})
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

// newWorkerTransport sends HTTP requests over new yamux streams.
func newWorkerTransport(ys *yamux.Session) *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return ys.Open()
		},
		MaxIdleConnsPerHost: 32,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true,
	}
}

// newWorkerProxy forwards browser requests to the worker. WebSocket
// upgrades pass through, which is how terminals reach the worker's PTYs;
// ending a request's context closes its WebSocket (locking does that).
func newWorkerProxy(transport http.RoundTripper) http.Handler {
	target := &url.URL{Scheme: "http", Host: "worker"}
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = "worker"
			// The worker trusts the server; it never needs the browser's
			// credentials, and only the server makes internal requests.
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Del(protocol.InternalHeader)
		},
		// Cookies are the server's: the worker is the user's process, and
		// must not set or replace them. Worker responses are not cached
		// unless the worker says so (thumbnails, file contents), so a
		// listing never outlives a lock in the browser's cache.
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Del("Set-Cookie")
			if resp.Header.Get("Cache-Control") == "" {
				resp.Header.Set("Cache-Control", "no-store")
			}
			return nil
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
