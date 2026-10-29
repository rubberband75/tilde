// Package server is the main Tilde process ("tilde serve").
//
// It terminates TLS, serves the frontend, authenticates browsers through the
// root session helper, and proxies /api/v1/* to the logged-in user's worker.
// It never touches user files itself and never changes its own UID: all of
// that happens in the helper and worker processes (see docs/architecture.md).
package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"tilde/internal/pamauth"
	"tilde/internal/protocol"
)

// Config holds the server's settings.
type Config struct {
	// Listen is host:port. The default binds to localhost only: reach Tilde
	// over a LAN address or a VPN (Tailscale, WireGuard) on purpose.
	Listen string
	// Dev serves plain HTTP on a loopback address for frontend development
	// behind the Vite dev server. Without root it also runs in single-user
	// mode (only the account that started the server can log in).
	Dev bool
	// TLSCert and TLSKey are PEM files. If empty, a self-signed certificate
	// is created under StateDir/tls.
	TLSCert, TLSKey string
	StateDir        string
	// AllowedOrigins lists extra origins (e.g. "https://tilde.example.com")
	// accepted by the Origin check, for setups behind a reverse proxy.
	AllowedOrigins []string
	// TrustProxy uses X-Forwarded-For for rate limiting. Only enable it
	// behind a reverse proxy you control.
	TrustProxy bool
	// SessionMaxAge is the absolute token lifetime: --session-lifetime,
	// 12 hours by default (D16, D69).
	SessionMaxAge time.Duration
	// Web is the built frontend.
	Web fs.FS
}

// Server wires the HTTP handlers together.
type Server struct {
	cfg      Config
	tokens   *TokenStore
	sessions *Manager
	limiter  *loginLimiter
	cookie   string
	secure   bool
	// login and verify check a password through the session helper, for
	// logging in and unlocking; tests replace them.
	login, verify func(ctx context.Context, user, password, remoteAddr string) (check, error)
	// pending holds checks waiting for the answer to a PAM question (D43).
	pending pendingStore
	// audit is the audit log (D46); anonymous bounds the records anyone
	// on the network can cause (failed logins), which the rate limit
	// alone does not when they come from many addresses.
	audit     *auditLog
	anonMu    sync.Mutex
	anonymous auditBudget
}

// New creates a Server.
func New(cfg Config) (*Server, error) {
	if cfg.SessionMaxAge == 0 {
		cfg.SessionMaxAge = 12 * time.Hour
	}
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("find own executable: %w", err)
	}
	s := &Server{
		cfg:     cfg,
		tokens:  NewTokenStore(cfg.SessionMaxAge),
		limiter: newLoginLimiter(5, 15*time.Minute),
		secure:  !cfg.Dev,
	}
	// The __Host- prefix makes the browser refuse the cookie unless it is
	// Secure, host-only and Path=/.
	s.cookie = "__Host-tilde"
	if !s.secure {
		s.cookie = "tilde_session"
	}
	s.sessions = NewManager(self, s.tokens.RevokeUser)
	s.login = func(ctx context.Context, user, password, addr string) (check, error) {
		return asCheck(s.sessions.Login(ctx, user, password, addr))
	}
	s.verify = func(ctx context.Context, user, password, addr string) (check, error) {
		return asCheck(s.sessions.Verify(ctx, user, password, addr))
	}
	s.pending.m = map[string]*pending{}
	s.audit = &auditLog{}
	s.sessions.audit = s.audit
	s.tokens.idleLocked = func(user string) {
		s.audit.record(auditRecord{Event: "lock", User: user, Text: "was locked after no input", Fields: [][2]string{{"REASON", "idle"}}})
	}
	s.tokens.lockAfter = s.lockAfter
	// logind's view of the session (D41): its lock requests lock every
	// browser, and its LockedHint follows whether all of them are locked.
	s.sessions.lockRequested = func(user string) {
		s.tokens.LockUser(user, "logind")
		s.audit.record(auditRecord{Event: "lock", User: user, Text: "was locked by logind", Fields: [][2]string{{"REASON", "logind"}}})
	}
	s.sessions.allLocked = s.tokens.AllLocked
	s.tokens.changed = func(user string) {
		if us := s.sessions.Get(user); us != nil {
			us.notifyLocked()
		}
	}
	return s, nil
}

// asCheck keeps a nil *Auth from becoming a non-nil check.
func asCheck(a *Auth, err error) (check, error) {
	if err != nil {
		return nil, err
	}
	return a, nil
}

// lockAfter is how long user's clients may go without input before they
// lock (D40); 0 never locks.
func (s *Server) lockAfter(user string) time.Duration {
	if us := s.sessions.Get(user); us != nil {
		return us.LockAfter()
	}
	return 0
}

// Handler returns the full HTTP handler chain.
func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("POST /api/auth/login", s.handleLogin)
	m.HandleFunc("POST /api/auth/logout", s.handleLogout)
	m.HandleFunc("POST /api/auth/leave", s.handleLeave)
	m.HandleFunc("GET /api/auth/session", s.handleSession)
	m.HandleFunc("POST /api/auth/activity", s.handleActivity)
	m.HandleFunc("POST /api/auth/lock", s.handleLock)
	m.HandleFunc("POST /api/auth/unlock", s.handleUnlock)
	m.HandleFunc("POST /api/auth/answer", s.handleAnswer)
	m.HandleFunc("GET /api/auth/clients", s.handleClients)
	m.HandleFunc("GET /api/auth/logins", s.handleLogins)
	m.HandleFunc("POST /api/auth/clients/signout", s.handleClientSignOut)
	m.HandleFunc("/api/v1/", s.handleWorkerAPI)
	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, apiError{Error: "no such endpoint", Code: "not_found"})
	})
	if s.cfg.Web != nil {
		m.Handle("/", spaHandler(s.cfg.Web))
	}
	hsts := !s.cfg.Dev && s.cfg.TLSCert != ""
	return securityHeaders(hsts)(originCheck(s.cfg.AllowedOrigins)(m))
}

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,31}\$?$`)

type sessionInfo struct {
	User string `json:"user"`
	UID  int    `json:"uid"`
	Home string `json:"home"`
	// Locked: this client shows the lock screen until it is unlocked.
	Locked bool `json:"locked"`
	lockTiming
}

// lockTiming tells the browser how its input is used for idle locking.
type lockTiming struct {
	// LockAfter is the seconds without input after which this client
	// locks; 0 never.
	LockAfter int `json:"lockAfter"`
	// Heartbeat is how often, in seconds, the browser reports input while
	// there is some.
	Heartbeat int `json:"heartbeat"`
}

func (s *Server) timing(user string) lockTiming {
	d := s.lockAfter(user)
	return lockTiming{LockAfter: int(d / time.Second), Heartbeat: int(heartbeat(d) / time.Second)}
}

func (s *Server) info(us *UserSession, locked bool) sessionInfo {
	return sessionInfo{User: us.User, UID: us.UID, Home: us.Home, Locked: locked, lockTiming: s.timing(us.User)}
}

// auditFailure records what anyone on the network can cause, within a
// budget shared by all of them.
func (s *Server) auditFailure(r auditRecord) {
	s.anonMu.Lock()
	ok, dropped := s.anonymous.allow(time.Now()), 0
	if ok {
		dropped, s.anonymous.dropped = s.anonymous.dropped, 0
	}
	s.anonMu.Unlock()
	if !ok {
		return
	}
	if dropped > 0 {
		r.Fields = append(r.Fields, [2]string{"DROPPED", strconv.Itoa(dropped)})
	}
	s.audit.record(r)
}

// beginAttempt counts a password attempt from addr before it is checked,
// or answers 429 if addr has made too many.
func (s *Server) beginAttempt(w http.ResponseWriter, addr, user string) bool {
	ok, wait := s.limiter.Begin(addr, user)
	if !ok {
		s.auditFailure(auditRecord{Event: "rate_limited", User: user, Rhost: addr, Text: "was refused: too many attempts", Failed: true})
		mins := int(wait.Minutes()) + 1
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeJSON(w, http.StatusTooManyRequests, apiError{
			Error: fmt.Sprintf("Too many failed attempts. Try again in %d minute(s).", mins),
			Code:  "rate_limited",
		})
	}
	return ok
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	addr := clientAddr(r, s.cfg.TrustProxy)
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	if err := jsonDecode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid request", Code: "bad_request"})
		return
	}
	if !s.beginAttempt(w, addr, req.Username) {
		return
	}
	if !usernameRe.MatchString(req.Username) || req.Password == "" {
		s.auditFailure(auditRecord{Event: "login_failed", User: req.Username, Rhost: addr, Text: "failed to log in", Failed: true,
			Fields: [][2]string{{"REASON", protocol.CodeAuthFailed}}})
		writeJSON(w, http.StatusUnauthorized, apiError{Error: "Incorrect user name or password.", Code: protocol.CodeAuthFailed})
		return
	}

	s.dropPending(r)
	ctx, cancel := checkContext(r)
	defer cancel()
	c, err := s.login(ctx, req.Username, req.Password, addr)
	req.Password = ""
	p := &pending{check: c, user: req.Username, addr: addr}
	if err != nil {
		s.checkFailed(w, p, err, "Incorrect user name or password.")
		return
	}
	s.carryOn(w, r, p)
}

func (s *Server) sessionCookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     s.cookie,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteStrictMode,
	}
}

func (s *Server) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, s.sessionCookie("", -1))
}

// cookieToken returns the request's cookie value and what it stands for.
func (s *Server) cookieToken(r *http.Request) (string, Token, bool) {
	c, err := r.Cookie(s.cookie)
	if err != nil {
		return "", Token{}, false
	}
	t, ok := s.tokens.Lookup(c.Value)
	return c.Value, t, ok
}

// session returns the running session for a valid token, or answers the
// request: 401 not_logged_in without a valid token, 401 session_ended if
// the session is gone. The explicit code lets the frontend return to the
// login screen instead of retrying forever (a lesson from webdesktopd's
// reconnect bugs).
func (s *Server) session(w http.ResponseWriter, t Token, ok bool) *UserSession {
	if !ok {
		writeJSON(w, http.StatusUnauthorized, apiError{Error: "not logged in", Code: "not_logged_in"})
		return nil
	}
	us := s.sessions.Get(t.User)
	if us == nil {
		s.tokens.RevokeUser(t.User)
		s.clearCookie(w)
		writeJSON(w, http.StatusUnauthorized, apiError{Error: "your session has ended", Code: "session_ended"})
	}
	return us
}

// clearCache asks the browser to drop its HTTP cache for Tilde's origin,
// where file contents and thumbnails from the worker may sit: a locked or
// signed-out browser must not show them (D40).
func clearCache(w http.ResponseWriter) {
	w.Header().Set("Clear-Site-Data", `"cache"`)
}

func writeLocked(w http.ResponseWriter) {
	clearCache(w)
	writeJSON(w, http.StatusLocked, apiError{Error: "this screen is locked", Code: "locked"})
}

// handleLogout ends the whole session, matching Mint's Log Out (D15):
// terminals get SIGHUP, the worker exits, the PAM session closes, and every
// device logged in as this user is signed out. From a locked client it
// only signs that client out: otherwise anyone at a locked screen could end
// the session everywhere.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	tok, t, ok := s.cookieToken(r)
	s.clearCookie(w)
	clearCache(w)
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if t.Locked {
		s.tokens.Revoke(tok)
		slog.Info("left a locked screen", "user", t.User)
		s.audit.record(auditRecord{Event: "leave", User: t.User, Rhost: clientAddr(r, s.cfg.TrustProxy), Text: "signed a locked browser out"})
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.tokens.RevokeUser(t.User)
	s.sessions.End(t.User)
	slog.Info("logout", "user", t.User)
	s.audit.record(auditRecord{Event: "logout", User: t.User, Rhost: clientAddr(r, s.cfg.TrustProxy), Text: "logged out"})
	w.WriteHeader(http.StatusNoContent)
}

// handleLeave signs this browser out and leaves the session running (the
// lock screen's Switch User). Unlike logout it never ends the session,
// whatever state the cookie is in: another tab may have unlocked it.
func (s *Server) handleLeave(w http.ResponseWriter, r *http.Request) {
	s.clearCookie(w)
	clearCache(w)
	if tok, t, ok := s.cookieToken(r); ok {
		s.tokens.Revoke(tok)
		slog.Info("signed out one browser", "user", t.User)
		s.audit.record(auditRecord{Event: "leave", User: t.User, Rhost: clientAddr(r, s.cfg.TrustProxy), Text: "signed a browser out"})
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSession says who is logged in, and whether this client is locked.
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	_, t, ok := s.cookieToken(r)
	if us := s.session(w, t, ok); us != nil {
		if t.Locked {
			clearCache(w)
		}
		writeJSON(w, http.StatusOK, s.info(us, t.Locked))
	}
}

// handleActivity records keyboard or pointer input in the browser, which
// keeps the client from locking (D40).
func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(s.cookie)
	var t Token
	ok := err == nil
	if ok {
		t, ok = s.tokens.Touch(c.Value)
	}
	us := s.session(w, t, ok)
	switch {
	case us == nil:
	case t.Locked:
		writeLocked(w)
	default:
		writeJSON(w, http.StatusOK, s.timing(t.User))
	}
}

// handleLock locks every client of the user, as the Lock Screen button
// does on Mint, where there is one screen. A locked client cannot: it
// could keep locking the user's other browsers.
func (s *Server) handleLock(w http.ResponseWriter, r *http.Request) {
	_, t, ok := s.cookieToken(r)
	switch us := s.session(w, t, ok); {
	case us == nil:
	case t.Locked:
		writeLocked(w)
	default:
		s.tokens.LockUser(t.User, "locked by the user")
		s.audit.record(auditRecord{Event: "lock", User: t.User, Rhost: clientAddr(r, s.cfg.TrustProxy), Text: "locked the screen",
			Fields: [][2]string{{"REASON", "manual"}}})
		clearCache(w)
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleUnlock checks the password of the token's user through the
// session helper (PAM auth and account stacks, like a login) and replaces
// the token with a new, unlocked one. Other clients stay locked. Attempts
// count towards the login rate limit, and a token gets maxUnlockTries:
// after that the browser must log in again, so copying a locked cookie to
// other addresses does not buy more guesses.
func (s *Server) handleUnlock(w http.ResponseWriter, r *http.Request) {
	tok, t, ok := s.cookieToken(r)
	us := s.session(w, t, ok)
	if us == nil {
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	if err := jsonDecode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid request", Code: "bad_request"})
		return
	}
	addr := clientAddr(r, s.cfg.TrustProxy)
	if !s.beginAttempt(w, addr, t.User) {
		return
	}
	if !s.tokens.BeginUnlock(tok) {
		slog.Warn("unlock: too many attempts, signed the browser out", "user", t.User, "rhost", addr)
		s.audit.record(auditRecord{Event: "unlock_failed", User: t.User, Rhost: addr, Text: "was signed out after too many wrong passwords",
			Failed: true, Fields: [][2]string{{"REASON", "too_many_attempts"}}})
		s.clearCookie(w)
		clearCache(w)
		writeJSON(w, http.StatusUnauthorized, apiError{Error: "Too many wrong passwords. Log in again.", Code: "too_many_attempts"})
		return
	}
	if req.Password == "" {
		writeJSON(w, http.StatusUnauthorized, apiError{Error: "Incorrect password.", Code: protocol.CodeAuthFailed})
		return
	}

	s.dropPending(r)
	ctx, cancel := checkContext(r)
	defer cancel()
	c, err := s.verify(ctx, t.User, req.Password, addr)
	req.Password = ""
	p := &pending{check: c, unlock: true, user: t.User, addr: addr}
	if err != nil {
		s.checkFailed(w, p, err, "Incorrect password.")
		return
	}
	s.carryOn(w, r, p)
}

// userAgent is the browser's own description, for the list of a user's
// browsers (D47); it is only shown, never trusted.
func userAgent(r *http.Request) string {
	ua := r.UserAgent()
	if len(ua) > 256 {
		ua = strings.ToValidUTF8(ua[:256], "")
	}
	return ua
}

// handleClients lists the user's signed-in browsers, for the panel (D47).
// A locked browser cannot: it would show where the user is.
func (s *Server) handleClients(w http.ResponseWriter, r *http.Request) {
	tok, t, ok := s.cookieToken(r)
	switch us := s.session(w, t, ok); {
	case us == nil:
	case t.Locked:
		writeLocked(w)
	default:
		writeJSON(w, http.StatusOK, map[string][]Client{"clients": s.tokens.Clients(t.User, tok)})
	}
}

// handleClientSignOut signs out another of the user's browsers, one left
// open elsewhere. This browser signs itself out with Log Out or the lock
// screen's Switch User instead.
func (s *Server) handleClientSignOut(w http.ResponseWriter, r *http.Request) {
	tok, t, ok := s.cookieToken(r)
	us := s.session(w, t, ok)
	if us == nil {
		return
	}
	if t.Locked {
		writeLocked(w)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := jsonDecode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid request", Code: "bad_request"})
		return
	}
	for _, c := range s.tokens.Clients(t.User, tok) {
		if c.ID == req.ID && c.Current {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "this is the browser you are using; log out instead", Code: "bad_request"})
			return
		}
	}
	addr, found := s.tokens.RevokeID(t.User, req.ID)
	if !found {
		writeJSON(w, http.StatusNotFound, apiError{Error: "that browser is not signed in", Code: "not_found"})
		return
	}
	slog.Info("signed out another browser", "user", t.User, "client", addr)
	s.audit.record(auditRecord{Event: "signout_client", User: t.User, Rhost: clientAddr(r, s.cfg.TrustProxy),
		Text: "signed out the browser at " + addr, Fields: [][2]string{{"CLIENT", addr}}})
	w.WriteHeader(http.StatusNoContent)
}

// handleWorkerAPI authenticates the request and forwards it to the worker.
// A locked client gets 423 "locked". The request ends when its token is
// locked or revoked, which closes WebSockets (terminals, events) too.
func (s *Server) handleWorkerAPI(w http.ResponseWriter, r *http.Request) {
	tok, t, ok := s.cookieToken(r)
	us := s.session(w, t, ok)
	if us == nil {
		return
	}
	if t.Locked {
		writeLocked(w)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(t.Context, cancel)
	defer stop()
	// Where the request came from, for the worker's audit events (D46);
	// whatever the browser sent in its place is replaced.
	r.Header.Set(protocol.ClientHeader, clientAddr(r, s.cfg.TrustProxy))
	// A tab showing the desktop keeps the events socket open (D47).
	if r.URL.Path == "/api/v1/events" && strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		defer s.tokens.Connect(tok)()
	}
	us.ServeHTTP(w, r.WithContext(ctx))
}

// Run serves until ctx is cancelled, then ends every session.
func Run(ctx context.Context, cfg Config) error {
	host, _, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		return fmt.Errorf("invalid listen address %q: %w", cfg.Listen, err)
	}
	if cfg.Dev {
		if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return errors.New("--dev serves plain HTTP and only listens on a loopback address (e.g. 127.0.0.1:8443)")
		}
		slog.Warn("development mode: plain HTTP on loopback only")
	} else if os.Geteuid() != 0 {
		return errors.New("tilde serve must run as root to authenticate users and start their sessions (use --dev for single-user development)")
	}
	if os.Geteuid() != 0 {
		slog.Warn("not running as root: single-user mode, only the current account can log in")
	}
	if !pamauth.Available {
		slog.Error("this build has no PAM support; every login will fail (build with CGO_ENABLED=1 and libpam0g-dev)")
	}

	s, err := New(cfg)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(slog.Default().Handler(), slog.LevelWarn),
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return err
	}
	errc := make(chan error, 1)
	if cfg.Dev {
		slog.Info("listening", "url", "http://"+ln.Addr().String())
		go func() { errc <- srv.Serve(ln) }()
	} else {
		cert, err := loadOrCreateCert(cfg.TLSCert, cfg.TLSKey, cfg.StateDir, host)
		if err != nil {
			ln.Close()
			return fmt.Errorf("TLS certificate: %w", err)
		}
		srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
		slog.Info("listening", "url", "https://"+ln.Addr().String())
		go func() { errc <- srv.ServeTLS(ln, "", "") }()
	}

	// Often enough that an idle client loses its WebSockets close to its
	// idle delay, cheap with a handful of tokens.
	sweep := time.NewTicker(5 * time.Second)
	defer sweep.Stop()
	for {
		select {
		case err := <-errc:
			s.sessions.EndAll()
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		case <-sweep.C:
			s.tokens.Sweep()
		case <-ctx.Done():
			slog.Info("shutting down")
			shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = srv.Shutdown(shutCtx)
			cancel()
			s.sessions.EndAll()
			return nil
		}
	}
}
