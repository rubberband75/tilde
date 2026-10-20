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
	return s, nil
}

// Handler returns the full HTTP handler chain.
func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("POST /api/auth/login", s.handleLogin)
	m.HandleFunc("POST /api/auth/logout", s.handleLogout)
	m.HandleFunc("GET /api/auth/session", s.handleSession)
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
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	addr := clientAddr(r, s.cfg.TrustProxy)
	if ok, wait := s.limiter.Allowed(addr); !ok {
		mins := int(wait.Minutes()) + 1
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeJSON(w, http.StatusTooManyRequests, apiError{
			Error: fmt.Sprintf("Too many failed attempts. Try again in %d minute(s).", mins),
			Code:  "rate_limited",
		})
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	if err := jsonDecode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid request", Code: "bad_request"})
		return
	}
	if !usernameRe.MatchString(req.Username) || req.Password == "" {
		s.limiter.Fail(addr)
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
				s.limiter.Fail(addr)
				status = http.StatusUnauthorized
				msg = "Incorrect user name or password."
			}
			writeJSON(w, status, apiError{Error: msg, Code: ae.Code})
			return
		}
		slog.Error("login: could not start session", "user", req.Username, "err", err)
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "Could not start your session. See the server log.", Code: protocol.CodeInternal})
		return
	}
	s.limiter.Succeed(addr)

	tok, err := s.tokens.New(us.User)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "could not create a session token", Code: protocol.CodeInternal})
		return
	}
	http.SetCookie(w, s.sessionCookie(tok, int(s.cfg.SessionMaxAge.Seconds())))
	slog.Info("login", "user", us.User, "rhost", addr)
	writeJSON(w, http.StatusOK, sessionInfo{User: us.User, UID: us.UID, Home: us.Home})
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

// currentUser returns the user for the request's cookie.
func (s *Server) currentUser(r *http.Request) (string, bool) {
	c, err := r.Cookie(s.cookie)
	if err != nil {
		return "", false
	}
	return s.tokens.Lookup(c.Value)
}

// handleLogout ends the whole session, matching Mint's Log Out (D15):
// terminals get SIGHUP, the worker exits, the PAM session closes, and every
// device logged in as this user is signed out.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(r)
	s.clearCookie(w)
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.tokens.RevokeUser(user)
	s.sessions.End(user)
	slog.Info("logout", "user", user)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, apiError{Error: "not logged in", Code: "not_logged_in"})
		return
	}
	us := s.sessions.Get(user)
	if us == nil {
		s.tokens.RevokeUser(user)
		s.clearCookie(w)
		writeJSON(w, http.StatusUnauthorized, apiError{Error: "your session has ended", Code: "session_ended"})
		return
	}
	writeJSON(w, http.StatusOK, sessionInfo{User: us.User, UID: us.UID, Home: us.Home})
}

// handleWorkerAPI authenticates the request and forwards it to the worker.
// A token whose session is gone gets the explicit "session_ended" code, so
// the frontend can return to the login screen instead of retrying forever
// (a lesson from webdesktopd's reconnect bugs).
func (s *Server) handleWorkerAPI(w http.ResponseWriter, r *http.Request) {
	user, ok := s.currentUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, apiError{Error: "not logged in", Code: "not_logged_in"})
		return
	}
	us := s.sessions.Get(user)
	if us == nil {
		s.tokens.RevokeUser(user)
		s.clearCookie(w)
		writeJSON(w, http.StatusUnauthorized, apiError{Error: "your session has ended", Code: "session_ended"})
		return
	}
	us.ServeHTTP(w, r)
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

	sweep := time.NewTicker(10 * time.Minute)
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
