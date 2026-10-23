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
	// SessionMaxAge is the absolute token lifetime (D16: 12h).
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
	// verify checks a user's password through the session helper (for
	// unlocking); tests replace it.
	verify func(ctx context.Context, user, password, remoteAddr string) error
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
	s.verify = s.sessions.Verify
	s.tokens.lockAfter = s.lockAfter
	return s, nil
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
	m.HandleFunc("/api/v1/", s.handleWorkerAPI)
	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, apiError{Error: "no such endpoint", Code: "not_found"})
	})
	if s.cfg.Web != nil {
		m.Handle("/", spaHandler(s.cfg.Web))
	}
	return securityHeaders(originCheck(s.cfg.AllowedOrigins)(m))
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

// beginAttempt counts a password attempt from addr before it is checked,
// or answers 429 if addr has made too many.
func (s *Server) beginAttempt(w http.ResponseWriter, addr, user string) bool {
	ok, wait := s.limiter.Begin(addr, user)
	if !ok {
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
		writeJSON(w, http.StatusUnauthorized, apiError{Error: "Incorrect user name or password.", Code: protocol.CodeAuthFailed})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	us, err := s.sessions.Login(ctx, req.Username, req.Password, addr)
	req.Password = ""
	if err != nil {
		var ae *AuthError
		if errors.As(err, &ae) {
			status := http.StatusForbidden
			msg := ae.Message
			if ae.Code == protocol.CodeAuthFailed {
				status = http.StatusUnauthorized
				msg = "Incorrect user name or password."
			} else {
				s.limiter.Cancel(addr, req.Username)
			}
			writeJSON(w, status, apiError{Error: msg, Code: ae.Code})
			return
		}
		s.limiter.Cancel(addr, req.Username)
		slog.Error("login: could not start session", "user", req.Username, "err", err)
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "Could not start your session. See the server log.", Code: protocol.CodeInternal})
		return
	}
	s.limiter.Succeed(addr, req.Username)

	tok, err := s.tokens.New(us.User)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "could not create a session token", Code: protocol.CodeInternal})
		return
	}
	// The new cookie replaces this browser's old one; drop its token too.
	if old, err := r.Cookie(s.cookie); err == nil {
		s.tokens.Revoke(old.Value)
	}
	http.SetCookie(w, s.sessionCookie(tok, int(s.cfg.SessionMaxAge.Seconds())))
	slog.Info("login", "user", us.User, "rhost", addr)
	writeJSON(w, http.StatusOK, s.info(us, false))
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
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.tokens.RevokeUser(t.User)
	s.sessions.End(t.User)
	slog.Info("logout", "user", t.User)
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
		s.clearCookie(w)
		clearCache(w)
		writeJSON(w, http.StatusUnauthorized, apiError{Error: "Too many wrong passwords. Log in again.", Code: "too_many_attempts"})
		return
	}
	if req.Password == "" {
		writeJSON(w, http.StatusUnauthorized, apiError{Error: "Incorrect password.", Code: protocol.CodeAuthFailed})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	err := s.verify(ctx, t.User, req.Password, addr)
	req.Password = ""
	if err != nil {
		var ae *AuthError
		if errors.As(err, &ae) {
			status := http.StatusForbidden
			msg := ae.Message
			if ae.Code == protocol.CodeAuthFailed {
				status = http.StatusUnauthorized
				msg = "Incorrect password."
			} else {
				s.limiter.Cancel(addr, t.User)
			}
			slog.Warn("unlock failed", "user", t.User, "rhost", addr, "code", ae.Code)
			writeJSON(w, status, apiError{Error: msg, Code: ae.Code})
			return
		}
		s.limiter.Cancel(addr, t.User)
		slog.Error("unlock: could not check the password", "user", t.User, "err", err)
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "Could not check your password. See the server log.", Code: protocol.CodeInternal})
		return
	}
	s.limiter.Succeed(addr, t.User)

	fresh, ok := s.tokens.Rotate(tok)
	if !ok {
		// Not clearing the cookie: another tab of this browser may have
		// unlocked at the same moment and set a fresh one.
		writeJSON(w, http.StatusUnauthorized, apiError{Error: "not logged in", Code: "not_logged_in"})
		return
	}
	http.SetCookie(w, s.sessionCookie(fresh, int(s.cfg.SessionMaxAge.Seconds())))
	slog.Info("unlock", "user", t.User, "rhost", addr)
	writeJSON(w, http.StatusOK, s.info(us, false))
}

// handleWorkerAPI authenticates the request and forwards it to the worker.
// A locked client gets 423 "locked". The request ends when its token is
// locked or revoked, which closes WebSockets (terminals, events) too.
func (s *Server) handleWorkerAPI(w http.ResponseWriter, r *http.Request) {
	_, t, ok := s.cookieToken(r)
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
