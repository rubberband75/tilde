package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"

	"tilde/internal/mux"
	"tilde/internal/protocol"
)

// lockTest is a server with a running session for alice, whose worker is
// a handler, a fake clock, and a password check that accepts "secret".
type lockTest struct {
	t   *testing.T
	s   *Server
	h   http.Handler
	us  *UserSession
	now time.Time
	// addr is the client address requests come from.
	addr string
}

func newLockTest(t *testing.T, worker http.Handler) *lockTest {
	t.Helper()
	s, err := New(Config{Listen: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	lt := &lockTest{t: t, s: s, h: s.Handler(), now: time.Now(), addr: "192.0.2.1:1234"}
	s.tokens.now = func() time.Time { return lt.now }
	s.verify = func(_ context.Context, user, password, _ string) error {
		if user == "alice" && password == "secret" {
			return nil
		}
		return &AuthError{Code: protocol.CodeAuthFailed, Message: "authentication failed"}
	}
	lt.us = lt.addSession("alice", worker)
	return lt
}

func (lt *lockTest) addSession(user string, worker http.Handler) *UserSession {
	us := &UserSession{User: user, UID: 1000, Home: "/home/" + user, proxy: worker, done: make(chan struct{})}
	d := 10 * time.Minute
	us.lockAfter.Store(&d)
	lt.s.sessions.sessions[user] = us
	return us
}

func (lt *lockTest) login(user string) string {
	lt.t.Helper()
	tok, err := lt.s.tokens.New(user)
	if err != nil {
		lt.t.Fatal(err)
	}
	return tok
}

// do sends a same-origin request with tok as the session cookie.
func (lt *lockTest) do(method, path, tok, body string) *httptest.ResponseRecorder {
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, "http://desk.lan"+path, r)
	req.Header.Set("Origin", "http://desk.lan")
	req.RemoteAddr = lt.addr
	if tok != "" {
		req.AddCookie(&http.Cookie{Name: lt.s.cookie, Value: tok})
	}
	w := httptest.NewRecorder()
	lt.h.ServeHTTP(w, req)
	return w
}

// expect checks a response's status and, if code is set, its error code.
func (lt *lockTest) expect(w *httptest.ResponseRecorder, status int, code string) {
	lt.t.Helper()
	if w.Code != status {
		lt.t.Fatalf("status %d, want %d: %s", w.Code, status, w.Body)
	}
	if code != "" {
		var e apiError
		_ = json.Unmarshal(w.Body.Bytes(), &e)
		if e.Code != code {
			lt.t.Fatalf("code %q, want %q", e.Code, code)
		}
	}
}

func (lt *lockTest) info(tok string) sessionInfo {
	lt.t.Helper()
	w := lt.do(http.MethodGet, "/api/auth/session", tok, "")
	lt.expect(w, http.StatusOK, "")
	var info sessionInfo
	if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil {
		lt.t.Fatal(err)
	}
	return info
}

var noContent = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })

func TestIdleLock(t *testing.T) {
	lt := newLockTest(t, noContent)
	tok := lt.login("alice")
	idle := lt.login("alice")
	idleCtx := func() context.Context {
		got, _ := lt.s.tokens.Lookup(idle)
		return got.Context
	}()

	// Input is reported at most every heartbeat; locking allows for two.
	w := lt.do(http.MethodPost, "/api/auth/activity", tok, "")
	lt.expect(w, http.StatusOK, "")
	var timing lockTiming
	_ = json.Unmarshal(w.Body.Bytes(), &timing)
	if timing != (lockTiming{LockAfter: 600, Heartbeat: 15}) {
		t.Fatalf("timing %+v", timing)
	}

	// Requests are not input; only reported activity keeps a client open.
	lt.now = lt.now.Add(10 * time.Minute)
	lt.expect(lt.do(http.MethodGet, "/api/v1/hello", tok, ""), http.StatusNoContent, "")
	lt.expect(lt.do(http.MethodPost, "/api/auth/activity", tok, ""), http.StatusOK, "")
	lt.now = lt.now.Add(10*time.Minute + 29*time.Second)
	lt.expect(lt.do(http.MethodGet, "/api/v1/hello", tok, ""), http.StatusNoContent, "")

	// The other client never reported input: the sweep locks it and ends
	// its requests without waiting for it to ask for anything.
	lt.s.tokens.Sweep()
	if idleCtx.Err() == nil {
		t.Fatal("sweep did not lock the idle client")
	}

	lt.now = lt.now.Add(2 * time.Second)
	lt.expect(lt.do(http.MethodGet, "/api/v1/hello", tok, ""), http.StatusLocked, "locked")
	if !lt.info(tok).Locked {
		t.Fatal("session does not say locked")
	}
	// Input after the fact does not unlock.
	lt.expect(lt.do(http.MethodPost, "/api/auth/activity", tok, ""), http.StatusLocked, "locked")
	lt.expect(lt.do(http.MethodGet, "/api/v1/hello", idle, ""), http.StatusLocked, "locked")
}

func TestIdleLockOff(t *testing.T) {
	lt := newLockTest(t, noContent)
	never := time.Duration(0)
	lt.us.lockAfter.Store(&never)
	tok := lt.login("alice")
	lt.now = lt.now.Add(11 * time.Hour)
	lt.s.tokens.Sweep()
	lt.expect(lt.do(http.MethodGet, "/api/v1/hello", tok, ""), http.StatusNoContent, "")
	if info := lt.info(tok); info.Locked || info.LockAfter != 0 || info.Heartbeat != 15 {
		t.Fatalf("info %+v", info)
	}
}

func TestLockScreenLocksEveryClient(t *testing.T) {
	lt := newLockTest(t, noContent)
	lt.addSession("bob", noContent)
	a, b, bob := lt.login("alice"), lt.login("alice"), lt.login("bob")

	lt.expect(lt.do(http.MethodPost, "/api/auth/lock", "", ""), http.StatusUnauthorized, "not_logged_in")
	lt.expect(lt.do(http.MethodPost, "/api/auth/lock", a, ""), http.StatusNoContent, "")
	lt.expect(lt.do(http.MethodGet, "/api/v1/hello", a, ""), http.StatusLocked, "locked")
	lt.expect(lt.do(http.MethodGet, "/api/v1/hello", b, ""), http.StatusLocked, "locked")
	lt.expect(lt.do(http.MethodGet, "/api/v1/hello", bob, ""), http.StatusNoContent, "")
	if info := lt.info(b); !info.Locked || info.User != "alice" {
		t.Fatalf("info %+v", info)
	}
}

func TestUnlock(t *testing.T) {
	lt := newLockTest(t, noContent)
	never := time.Duration(0) // only the 12-hour lifetime here
	lt.us.lockAfter.Store(&never)
	a, b := lt.login("alice"), lt.login("alice")
	lt.expect(lt.do(http.MethodPost, "/api/auth/lock", a, ""), http.StatusNoContent, "")

	lt.expect(lt.do(http.MethodPost, "/api/auth/unlock", a, `{"password":"wrong"}`), http.StatusUnauthorized, protocol.CodeAuthFailed)
	lt.expect(lt.do(http.MethodPost, "/api/auth/unlock", a, `{"password":""}`), http.StatusUnauthorized, protocol.CodeAuthFailed)
	lt.expect(lt.do(http.MethodGet, "/api/v1/hello", a, ""), http.StatusLocked, "locked")

	// The 12 hours start again: unlocking is typing the password.
	lt.now = lt.now.Add(11 * time.Hour)
	lt.expect(lt.do(http.MethodPost, "/api/auth/unlock", "", `{"password":"secret"}`), http.StatusUnauthorized, "not_logged_in")
	w := lt.do(http.MethodPost, "/api/auth/unlock", a, `{"password":"secret"}`)
	lt.expect(w, http.StatusOK, "")
	var fresh string
	for _, c := range w.Result().Cookies() {
		if c.Name == lt.s.cookie {
			fresh = c.Value
		}
	}
	if fresh == "" || fresh == a {
		t.Fatalf("unlock did not replace the cookie: %q", fresh)
	}
	lt.expect(lt.do(http.MethodGet, "/api/v1/hello", a, ""), http.StatusUnauthorized, "not_logged_in")
	lt.expect(lt.do(http.MethodGet, "/api/v1/hello", fresh, ""), http.StatusNoContent, "")
	lt.expect(lt.do(http.MethodGet, "/api/v1/hello", b, ""), http.StatusLocked, "locked")
	lt.now = lt.now.Add(2 * time.Hour)
	lt.expect(lt.do(http.MethodPost, "/api/auth/activity", fresh, ""), http.StatusOK, "")
	lt.expect(lt.do(http.MethodGet, "/api/v1/hello", b, ""), http.StatusUnauthorized, "not_logged_in")
}

func TestUnlockRateLimit(t *testing.T) {
	lt := newLockTest(t, noContent)
	a := lt.login("alice")
	lt.expect(lt.do(http.MethodPost, "/api/auth/lock", a, ""), http.StatusNoContent, "")
	for range 5 {
		lt.expect(lt.do(http.MethodPost, "/api/auth/unlock", a, `{"password":"guess"}`), http.StatusUnauthorized, protocol.CodeAuthFailed)
	}
	lt.expect(lt.do(http.MethodPost, "/api/auth/unlock", a, `{"password":"secret"}`), http.StatusTooManyRequests, "rate_limited")
}

// TestParallelUnlocks: attempts count before PAM answers, so a burst of
// guesses from a locked browser gets no more checks than the limit.
func TestParallelUnlocks(t *testing.T) {
	lt := newLockTest(t, noContent)
	var checks atomic.Int32
	lt.s.verify = func(context.Context, string, string, string) error {
		checks.Add(1)
		time.Sleep(50 * time.Millisecond) // PAM's delay after a failure
		return &AuthError{Code: protocol.CodeAuthFailed, Message: "authentication failed"}
	}
	// Many locked browsers at one address, so the per-token limit is not
	// what stops them.
	toks := make([]string, 20)
	for i := range toks {
		toks[i] = lt.login("alice")
	}
	lt.expect(lt.do(http.MethodPost, "/api/auth/lock", toks[0], ""), http.StatusNoContent, "")
	var wg sync.WaitGroup
	for _, tok := range toks {
		for range maxUnlockTries {
			wg.Add(1)
			go func() {
				defer wg.Done()
				lt.do(http.MethodPost, "/api/auth/unlock", tok, `{"password":"guess"}`)
			}()
		}
	}
	wg.Wait()
	if n := checks.Load(); n > 5 {
		t.Fatalf("%d password checks, want at most 5", n)
	}
}

// TestUnlockTriesPerToken: copying a locked cookie to other addresses
// does not buy more guesses; past the limit the browser must log in.
func TestUnlockTriesPerToken(t *testing.T) {
	lt := newLockTest(t, noContent)
	a := lt.login("alice")
	lt.expect(lt.do(http.MethodPost, "/api/auth/lock", a, ""), http.StatusNoContent, "")
	for i := range maxUnlockTries {
		lt.addr = fmt.Sprintf("198.51.100.%d:1234", i)
		lt.expect(lt.do(http.MethodPost, "/api/auth/unlock", a, `{"password":"guess"}`), http.StatusUnauthorized, protocol.CodeAuthFailed)
	}
	lt.addr = "203.0.113.9:1234"
	lt.expect(lt.do(http.MethodPost, "/api/auth/unlock", a, `{"password":"secret"}`), http.StatusUnauthorized, "too_many_attempts")
	lt.expect(lt.do(http.MethodGet, "/api/auth/session", a, ""), http.StatusUnauthorized, "not_logged_in")
	if lt.s.sessions.Get("alice") == nil {
		t.Fatal("the session ended")
	}
}

// TestLockedBrowserCannotLock: a locked browser could otherwise keep
// locking the user's other browsers.
func TestLockedBrowserCannotLock(t *testing.T) {
	lt := newLockTest(t, noContent)
	a, b := lt.login("alice"), lt.login("alice")
	lt.expect(lt.do(http.MethodPost, "/api/auth/lock", a, ""), http.StatusNoContent, "")
	w := lt.do(http.MethodPost, "/api/auth/unlock", b, `{"password":"secret"}`)
	lt.expect(w, http.StatusOK, "")
	b = w.Result().Cookies()[0].Value
	lt.expect(lt.do(http.MethodPost, "/api/auth/lock", a, ""), http.StatusLocked, "locked")
	lt.expect(lt.do(http.MethodGet, "/api/v1/hello", b, ""), http.StatusNoContent, "")
}

// TestLockClearsCache: file contents and thumbnails may sit in the
// browser's HTTP cache; a locked or signed-out browser is told to drop it.
func TestLockClearsCache(t *testing.T) {
	lt := newLockTest(t, noContent)
	a, b := lt.login("alice"), lt.login("alice")
	cleared := func(w *httptest.ResponseRecorder) bool { return w.Header().Get("Clear-Site-Data") == `"cache"` }
	if cleared(lt.do(http.MethodGet, "/api/v1/hello", a, "")) || cleared(lt.do(http.MethodGet, "/api/auth/session", a, "")) {
		t.Fatal("cache cleared while unlocked")
	}
	if !cleared(lt.do(http.MethodPost, "/api/auth/lock", a, "")) {
		t.Fatal("lock did not clear the cache")
	}
	for _, w := range []*httptest.ResponseRecorder{
		lt.do(http.MethodGet, "/api/v1/hello", b, ""),
		lt.do(http.MethodGet, "/api/auth/session", b, ""),
		lt.do(http.MethodPost, "/api/auth/leave", b, ""),
		lt.do(http.MethodPost, "/api/auth/logout", a, ""),
	} {
		if !cleared(w) {
			t.Fatalf("%d %s: no Clear-Site-Data", w.Code, w.Body)
		}
	}
}

// fakeWorker serves the worker's internal routes: the session stream
// sends lines, and LockedHint updates are recorded.
type fakeWorker struct {
	*httptest.Server
	lines []string
	mu    sync.Mutex
	hints []string
	fail  int // answer this many hint updates with 502 first
}

func newFakeWorker(t *testing.T, lines ...string) *fakeWorker {
	f := &fakeWorker{lines: lines}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(protocol.InternalHeader) == "" {
			http.NotFound(w, r)
			return
		}
		switch r.URL.Path {
		case "/internal/session":
			for _, l := range f.lines {
				io.WriteString(w, l)
			}
		case "/internal/session/locked":
			b, _ := io.ReadAll(r.Body)
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.fail > 0 {
				f.fail--
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			f.hints = append(f.hints, string(b))
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeWorker) client() *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", f.Listener.Addr().String())
		},
	}}
}

// TestSessionStream: the root server reads the worker, which the user
// controls, one short line at a time, clamps the delay, and passes on
// logind's lock requests.
func TestSessionStream(t *testing.T) {
	worker := newFakeWorker(t,
		`{"type":"settings","source":"cinnamon","lockAfter":99999999999}`+"\n",
		`{"type":"lock"}`+"\n",
		`{"type":"lock"}`+"\n", // a flood: one a second is passed on
		`{"type":"settings","source":"`+strings.Repeat("x", 1<<20))
	locks := 0
	us := &UserSession{User: "alice", done: make(chan struct{}), client: worker.client(), lockRequested: func() { locks++ }}
	if err := us.readSession(); !errors.Is(err, bufio.ErrTooLong) {
		t.Fatalf("err = %v, want a line too long", err)
	}
	if got := us.LockAfter(); got != 24*time.Hour {
		t.Fatalf("LockAfter = %v, want it clamped to a day", got)
	}
	if locks != 1 {
		t.Fatalf("%d lock requests passed on, want 1", locks)
	}
}

// TestLockedHint: logind's LockedHint says whether every browser of the
// user is locked, and logind's lock requests lock them all (D41). A failed
// update is tried again.
func TestLockedHint(t *testing.T) {
	lt := newLockTest(t, noContent)
	worker := newFakeWorker(t)
	worker.fail = 1
	us := lt.us
	us.client = worker.client()
	us.hint = make(chan struct{}, 1)
	us.allLocked = func() bool { return lt.s.tokens.AllLocked("alice") }
	go us.followLockedHint()
	t.Cleanup(func() { close(us.done) })
	hints := func(want ...string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			worker.mu.Lock()
			got := strings.Join(worker.hints, " ")
			worker.mu.Unlock()
			if got == strings.Join(want, " ") {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("hints %q, want %q", got, want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	a, b := lt.login("alice"), lt.login("alice")
	lt.expect(lt.do(http.MethodPost, "/api/auth/lock", a, ""), http.StatusNoContent, "")
	hints(`{"locked":true}`)
	w := lt.do(http.MethodPost, "/api/auth/unlock", b, `{"password":"secret"}`)
	lt.expect(w, http.StatusOK, "")
	hints(`{"locked":true}`, `{"locked":false}`)
	// Signing out the unlocked browser leaves only locked ones.
	lt.expect(lt.do(http.MethodPost, "/api/auth/leave", w.Result().Cookies()[0].Value, ""), http.StatusNoContent, "")
	hints(`{"locked":true}`, `{"locked":false}`, `{"locked":true}`)

	// "loginctl lock-session" locks every browser.
	c := lt.login("alice")
	hints(`{"locked":true}`, `{"locked":false}`, `{"locked":true}`, `{"locked":false}`)
	lt.s.sessions.lockRequested("alice")
	lt.expect(lt.do(http.MethodGet, "/api/v1/hello", c, ""), http.StatusLocked, "locked")
	hints(`{"locked":true}`, `{"locked":false}`, `{"locked":true}`, `{"locked":false}`, `{"locked":true}`)
}

// TestLockedHintRetry: a failed update is tried again on its own, with no
// further change to wake it.
func TestLockedHintRetry(t *testing.T) {
	worker := newFakeWorker(t)
	worker.fail = 1
	us := &UserSession{User: "alice", done: make(chan struct{}), client: worker.client(),
		hint: make(chan struct{}, 1), allLocked: func() bool { return true }}
	go us.followLockedHint()
	defer close(us.done)
	us.notifyLocked()
	deadline := time.Now().Add(5 * time.Second)
	for {
		worker.mu.Lock()
		got := strings.Join(worker.hints, " ")
		worker.mu.Unlock()
		if got == `{"locked":true}` {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("hints %q after a failed update", got)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestLockedLogoutOnlyLeaves: logging out from a locked screen must not
// end the session, or anyone at it could log the user out everywhere.
func TestLockedLogoutOnlyLeaves(t *testing.T) {
	lt := newLockTest(t, noContent)
	a, b := lt.login("alice"), lt.login("alice")
	lt.expect(lt.do(http.MethodPost, "/api/auth/lock", a, ""), http.StatusNoContent, "")
	lt.expect(lt.do(http.MethodPost, "/api/auth/logout", a, ""), http.StatusNoContent, "")
	if lt.s.sessions.Get("alice") == nil {
		t.Fatal("the session ended")
	}
	lt.expect(lt.do(http.MethodGet, "/api/auth/session", a, ""), http.StatusUnauthorized, "not_logged_in")
	lt.expect(lt.do(http.MethodGet, "/api/v1/hello", b, ""), http.StatusLocked, "locked")
}

// TestLeave: Switch User signs one browser out, locked or not (another tab
// of the browser may have unlocked it), and never ends the session.
func TestLeave(t *testing.T) {
	lt := newLockTest(t, noContent)
	a, b := lt.login("alice"), lt.login("alice")
	lt.expect(lt.do(http.MethodPost, "/api/auth/leave", a, ""), http.StatusNoContent, "")
	lt.expect(lt.do(http.MethodPost, "/api/auth/leave", "", ""), http.StatusNoContent, "")
	if lt.s.sessions.Get("alice") == nil {
		t.Fatal("the session ended")
	}
	lt.expect(lt.do(http.MethodGet, "/api/auth/session", a, ""), http.StatusUnauthorized, "not_logged_in")
	lt.expect(lt.do(http.MethodGet, "/api/v1/hello", b, ""), http.StatusNoContent, "")
}

func TestLockEndsOpenRequests(t *testing.T) {
	started, ended := make(chan struct{}), make(chan struct{})
	lt := newLockTest(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(ended)
	}))
	a := lt.login("alice")
	go lt.do(http.MethodGet, "/api/v1/fs/usage", a, "")
	<-started
	lt.expect(lt.do(http.MethodPost, "/api/auth/lock", a, ""), http.StatusNoContent, "")
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("locking did not end the request")
	}
}

// TestLockClosesWebSocket runs the real proxy to a worker over yamux, as a
// session does: locking must close a terminal's WebSocket, and browsers
// can never make the server's internal requests.
func TestLockClosesWebSocket(t *testing.T) {
	serverEnd, workerEnd := net.Pipe()
	worker := http.NewServeMux()
	worker.HandleFunc("/api/v1/echo", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer c.CloseNow()
		for {
			typ, b, err := c.Read(r.Context())
			if err != nil || c.Write(r.Context(), typ, b) != nil {
				return
			}
		}
	})
	worker.HandleFunc("/api/v1/headers", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, r.Header.Get(protocol.InternalHeader)+"|"+r.Header.Get("Cookie"))
	})
	ws, err := yamux.Server(workerEnd, mux.Config())
	if err != nil {
		t.Fatal(err)
	}
	go http.Serve(ws, worker)
	ys, err := yamux.Client(serverEnd, mux.Config())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ys.Close(); ws.Close() })

	lt := newLockTest(t, newWorkerProxy(newWorkerTransport(ys)))
	lt.now = time.Now()
	srv := httptest.NewServer(lt.h)
	defer srv.Close()
	a := lt.login("alice")

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/headers", nil)
	req.Header.Set(protocol.InternalHeader, "1")
	req.AddCookie(&http.Cookie{Name: lt.s.cookie, Value: a})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "|" {
		t.Fatalf("the worker got the browser's headers: %q", body)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := http.Header{}
	h.Set("Origin", srv.URL)
	h.Set("Cookie", lt.s.cookie+"="+a)
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/api/v1/echo", &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if err := c.Write(ctx, websocket.MessageText, []byte("hi")); err != nil {
		t.Fatal(err)
	}
	if _, b, err := c.Read(ctx); err != nil || string(b) != "hi" {
		t.Fatalf("echo: %q, %v", b, err)
	}

	lt.expect(lt.do(http.MethodPost, "/api/auth/lock", a, ""), http.StatusNoContent, "")
	if _, _, err := c.Read(ctx); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("the WebSocket stayed open after locking: %v", err)
	}
}
