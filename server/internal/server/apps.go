package server

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"tilde/internal/protocol"
)

// Local web apps (D66, D76): the user's own web servers (code-server,
// Jupyter, a development server) in a Tilde window, served from a second
// port so that their pages never run on Tilde's origin (rule 8).
//
// The browser comes to the app port with a cookie of its own, set by a
// one-time handoff from the desktop: the desktop asks for a code (POST
// /api/auth/apps/handoff) and posts it to the app port in a form, never in
// a URL. The app port takes Tilde's cookies out of what it passes on, and
// connects through the user's worker, which dials only the user's own
// ports. The main port turns away /api/ requests from other origins of
// the site, the app port's pages among them (fetchMetadataCheck).

// appsHandoffTTL is how long a handoff code may wait for its form.
const appsHandoffTTL = time.Minute

// Bounds on what logged-in browsers can make the server keep: codes
// waiting, and app cookies per desktop token (one per app window opened).
const (
	maxAppHandoffs   = 256
	maxHandoffsToken = 8
	maxAppsPerToken  = 32
)

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

type appHandoff struct {
	// main is the hash of the desktop's token.
	main string
	// origin is the desktop's: the form must come from there, and only
	// it may frame the app.
	origin  string
	expires time.Time
}

type appToken struct {
	main, origin string
}

// appAuth holds handoff codes and app cookies, by hash, in memory. An
// app cookie lasts while the desktop's token does: locking and unlocking
// replaces that token, and the desktop hands off again.
type appAuth struct {
	mu     sync.Mutex
	codes  map[string]appHandoff
	tokens map[string]appToken
}

func newAppAuth() *appAuth {
	return &appAuth{codes: map[string]appHandoff{}, tokens: map[string]appToken{}}
}

// add keeps an app cookie, dropping another of the same desktop token's
// past maxAppsPerToken.
func (a *appAuth) add(h string, t appToken) {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for k, o := range a.tokens {
		if o.main == t.main {
			if n++; n >= maxAppsPerToken {
				delete(a.tokens, k)
			}
		}
	}
	a.tokens[h] = t
}

// take removes and returns a code's handoff, if it has not expired.
func (a *appAuth) take(code string, now time.Time) (appHandoff, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	h := hashToken(code)
	ho, ok := a.codes[h]
	delete(a.codes, h)
	return ho, ok && now.Before(ho.expires)
}

// sweep drops expired codes, and app cookies whose desktop token is gone.
func (a *appAuth) sweep(now time.Time, valid func(main string) bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for h, ho := range a.codes {
		if !now.Before(ho.expires) {
			delete(a.codes, h)
		}
	}
	for h, t := range a.tokens {
		if !valid(t.main) {
			delete(a.tokens, h)
		}
	}
}

// appsAddress is where the app port listens: apps, or else the port after
// listen's on the same address; and how far it is from listen's port.
func appsAddress(listen, apps string) (string, int, error) {
	host, p, err := net.SplitHostPort(listen)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(p)
	if err != nil {
		return "", 0, fmt.Errorf("invalid listen port %q", p)
	}
	if apps == "" {
		next := port + 1
		if port == 0 { // any free port (tests)
			next = 0
		}
		return net.JoinHostPort(host, strconv.Itoa(next)), 1, nil
	}
	_, ap, err := net.SplitHostPort(apps)
	if err != nil {
		return "", 0, fmt.Errorf("invalid --apps-listen %q: %w", apps, err)
	}
	aport, err := strconv.Atoi(ap)
	if err != nil || aport < 1 || aport > 65535 || aport == port {
		return "", 0, fmt.Errorf("invalid --apps-listen port %q (another port than --listen's)", ap)
	}
	return apps, aport - port, nil
}

// appsOrigin is where browsers reach the app port: --apps-origin, else
// the host they reached Tilde at, on the port as far from theirs as the
// app port is from the main one (8443 and 8444; through a tunnel that
// forwards both, 9443 and 9444).
func (s *Server) appsOrigin(r *http.Request) string {
	if s.cfg.AppsOrigin != "" {
		return s.cfg.AppsOrigin
	}
	scheme, def := "https", 443
	if s.cfg.Dev {
		scheme, def = "http", 80
	}
	host, portStr, err := net.SplitHostPort(r.Host)
	port, perr := strconv.Atoi(portStr)
	if err != nil || perr != nil {
		host, port = r.Host, def
	}
	return scheme + "://" + net.JoinHostPort(host, strconv.Itoa(port+s.appsOffset))
}

// handleAppsHandoff gives the desktop a one-time code to post to the app
// port, and where that is.
func (s *Server) handleAppsHandoff(w http.ResponseWriter, r *http.Request) {
	if s.apps == nil {
		writeJSON(w, http.StatusNotFound, apiError{Error: "local web apps are turned off on this server", Code: "apps_off"})
		return
	}
	tok, t, ok := s.cookieToken(r)
	switch us := s.session(w, t, ok); {
	case us == nil:
	case t.Locked:
		writeLocked(w)
	default:
		code, err := randomToken()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, apiError{Error: "no randomness", Code: "internal"})
			return
		}
		// Never Tilde's own origin (rule 8): a misconfigured --apps-origin.
		if apps := s.appsOrigin(r); apps == r.Header.Get("Origin") {
			slog.Error("local web apps: --apps-origin is Tilde's own origin; refusing", "origin", apps)
			writeJSON(w, http.StatusInternalServerError, apiError{Error: "local web apps are misconfigured on this server", Code: "apps_misconfigured"})
			return
		}
		main := hashToken(tok)
		s.apps.mu.Lock()
		mine := 0
		for _, ho := range s.apps.codes {
			if ho.main == main {
				mine++
			}
		}
		// One browser cannot use up everyone's codes.
		full := len(s.apps.codes) >= maxAppHandoffs || mine >= maxHandoffsToken
		if !full {
			s.apps.codes[hashToken(code)] = appHandoff{main: main, origin: r.Header.Get("Origin"),
				expires: s.tokens.now().Add(appsHandoffTTL)}
		}
		s.apps.mu.Unlock()
		if full {
			writeJSON(w, http.StatusTooManyRequests, apiError{Error: "too many apps opening at once; try again", Code: "busy"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"code": code, "origin": s.appsOrigin(r)})
	}
}

// appsNext is where an app's handoff may send the browser: a path on the
// app port, never another site.
var appsNext = regexp.MustCompile(`^/(abs)?proxy/[1-9][0-9]{0,4}/[^\\\s]*$`)

// appsHandler serves the app port.
func (s *Server) appsHandler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("POST /_tilde/enter", s.handleAppEnter)
	m.HandleFunc("/proxy/{port}/{rest...}", s.handleAppProxy(false))
	m.HandleFunc("/absproxy/{port}/{rest...}", s.handleAppProxy(true))
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		appPage(w, http.StatusNotFound, "Nothing here", "Open local web apps from Tilde’s Web Apps window.")
	})
	return m
}

// handleAppEnter takes a handoff code from the desktop's form, sets the
// app cookie and goes on to the app.
func (s *Server) handleAppEnter(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		appPage(w, http.StatusBadRequest, "Invalid request", "Open the app again from Tilde.")
		return
	}
	next := r.PostForm.Get("next")
	ho, ok := s.apps.take(r.PostForm.Get("code"), s.tokens.now())
	switch {
	case !ok:
		appPage(w, http.StatusUnauthorized, "Open the app again", "This link has expired. Open the app again from Tilde.")
		return
	case !s.fromDesktop(r, ho):
		appPage(w, http.StatusForbidden, "Refused", "Apps open only from the Tilde that asked for them.")
		return
	case !appsNext.MatchString(next):
		appPage(w, http.StatusBadRequest, "Invalid request", "Open the app again from Tilde.")
		return
	}
	t, ok := s.tokens.LookupHash(ho.main)
	if !ok {
		appPage(w, http.StatusUnauthorized, "Not logged in", "Log in to Tilde, then open the app again.")
		return
	}
	if t.Locked {
		appPage(w, http.StatusLocked, "Tilde is locked", "Unlock Tilde to use this app.")
		return
	}
	value, err := randomToken()
	if err != nil {
		appPage(w, http.StatusInternalServerError, "Error", "Try again.")
		return
	}
	s.apps.add(hashToken(value), appToken{main: ho.main, origin: ho.origin})
	http.SetCookie(w, &http.Cookie{Name: s.appCookie(), Value: value, Path: "/", HttpOnly: true,
		Secure: s.secure, SameSite: http.SameSiteStrictMode})
	// Another user used apps in this browser: what their apps stored on the
	// app port (storage, service workers, cache) must not serve this one's.
	// Never "cookies": that would clear Tilde's too.
	owner := hashToken("app-owner:" + t.User)[:16]
	if c, err := r.Cookie(s.appCookie() + "-owner"); err == nil && c.Value != owner {
		w.Header().Set("Clear-Site-Data", `"storage", "cache"`)
	}
	http.SetCookie(w, &http.Cookie{Name: s.appCookie() + "-owner", Value: owner, Path: "/", MaxAge: 400 * 24 * 3600,
		HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteStrictMode})
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// fromDesktop reports whether the handoff form came from the browser and
// desktop that asked for the code.
//
// Where the app port is on Tilde's host, the browser sends Tilde's cookie
// with the form, and it must be the token the code was given to: a page
// elsewhere on the site cannot post a code of its own account's into
// another browser. The form also comes from the desktop's origin; but
// Tilde's pages send no Referer (Referrer-Policy: no-referrer), so
// browsers say "Origin: null", and then Fetch Metadata must say it came
// from this site. A browser that sends neither is refused.
func (s *Server) fromDesktop(r *http.Request, ho appHandoff) bool {
	if c, err := r.Cookie(s.cookie); err == nil && hashToken(c.Value) != ho.main {
		return false
	}
	switch origin := r.Header.Get("Origin"); origin {
	case "":
		return false
	case "null":
		return r.Header.Get("Sec-Fetch-Site") == "same-site"
	default:
		return origin == ho.origin
	}
}

func (s *Server) appCookie() string { return s.cookie + "-app" }

// tildeCookie names the cookies the app port keeps from apps: Tilde's own.
func (s *Server) tildeCookie(name string) bool {
	return name == s.cookie || strings.HasPrefix(name, s.cookie+"-")
}

// errAppNotListening: the worker found no socket of the user's on the port.
var errAppNotListening = errors.New("not listening")

// handleAppProxy passes a request to the user's app on {port}: with the
// prefix taken off (/proxy/, for apps that use relative links), or kept
// (/absproxy/, for apps told their base path).
func (s *Server) handleAppProxy(keepPrefix bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		port, err := strconv.Atoi(r.PathValue("port"))
		if err != nil || port < 1 || port > 65535 {
			appPage(w, http.StatusNotFound, "No such app", "That is not a port.")
			return
		}
		// Another site's pages, or a page elsewhere on this site doing more
		// than opening the app, do not get to use the app cookie.
		switch r.Header.Get("Sec-Fetch-Site") {
		case "cross-site":
			appPage(w, http.StatusForbidden, "Refused", "Another site cannot use your apps.")
			return
		case "same-site":
			if r.Header.Get("Sec-Fetch-Mode") != "navigate" {
				appPage(w, http.StatusForbidden, "Refused", "Another page cannot use your apps.")
				return
			}
		}
		var at appToken
		found := false
		if c, err := r.Cookie(s.appCookie()); err == nil {
			s.apps.mu.Lock()
			at, found = s.apps.tokens[hashToken(c.Value)]
			s.apps.mu.Unlock()
		}
		var t Token
		if found {
			t, found = s.tokens.LookupHash(at.main)
		}
		if !found {
			appPage(w, http.StatusUnauthorized, "Not logged in", "Open this app from Tilde’s Web Apps window.")
			return
		}
		if t.Locked {
			appPage(w, http.StatusLocked, "Tilde is locked", "Unlock Tilde to use this app.")
			return
		}
		us := s.sessions.Get(t.User)
		if us == nil {
			appPage(w, http.StatusUnauthorized, "Not logged in", "Your session has ended. Log in to Tilde again.")
			return
		}
		// Locking or signing out ends the app's requests and WebSockets.
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		stop := context.AfterFunc(t.Context, cancel)
		defer stop()
		s.appProxy(us, port, keepPrefix, at.origin).ServeHTTP(w, r.WithContext(ctx))
	}
}

func (s *Server) appProxy(us *UserSession, port int, keepPrefix bool, desktop string) http.Handler {
	prefix := "/proxy/" + strconv.Itoa(port)
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(&url.URL{Scheme: "http", Host: "127.0.0.1:" + strconv.Itoa(port)})
			// Only what Tilde says: not the browser's own.
			pr.Out.Header.Del("X-Forwarded-Prefix")
			pr.Out.Header.Del("X-Real-IP")
			if !keepPrefix {
				pr.Out.URL.Path = "/" + strings.TrimPrefix(strings.TrimPrefix(pr.In.URL.Path, prefix), "/")
				pr.Out.URL.RawPath = ""
				if raw := pr.In.URL.RawPath; raw != "" {
					pr.Out.URL.RawPath = "/" + strings.TrimPrefix(strings.TrimPrefix(raw, prefix), "/")
				}
				pr.Out.Header.Set("X-Forwarded-Prefix", prefix)
			} else {
				pr.Out.URL.Path, pr.Out.URL.RawPath = pr.In.URL.Path, pr.In.URL.RawPath
			}
			pr.SetXForwarded()
			// The browser's host, as apps that check WebSocket origins
			// against it (Jupyter, code-server) expect.
			pr.Out.Host = pr.In.Host
			// The app's cookies as the browser sent them; Tilde's out.
			var keep []string
			for _, line := range pr.In.Header.Values("Cookie") {
				for _, part := range strings.Split(line, ";") {
					part = strings.TrimSpace(part)
					if name, _, _ := strings.Cut(part, "="); name != "" && !s.tildeCookie(strings.TrimSpace(name)) {
						keep = append(keep, part)
					}
				}
			}
			pr.Out.Header.Del("Cookie")
			if len(keep) > 0 {
				pr.Out.Header.Set("Cookie", strings.Join(keep, "; "))
			}
			pr.Out.Header.Del(protocol.InternalHeader)
			pr.Out.Header.Del(protocol.ClientHeader)
		},
		ModifyResponse: func(resp *http.Response) error {
			appResponseHeaders(resp.Header, desktop, s.tildeCookie)
			return nil
		},
		Transport:     s.appTransport(us),
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if errors.Is(err, errAppNotListening) {
				appPage(w, http.StatusBadGateway, "Nothing on port "+strconv.Itoa(port),
					"None of your programs listens on this port now.")
				return
			}
			if !errors.Is(err, context.Canceled) {
				slog.Info("local web app", "user", us.User, "port", port, "err", err)
			}
			appPage(w, http.StatusBadGateway, "The app does not answer", "Port "+strconv.Itoa(port)+": "+err.Error())
		},
	}
}

// appResponseHeaders keeps an app from setting Tilde's cookies, and lets
// the desktop that opened it, and only that one, show it in a window: the
// app's own framing rules (X-Frame-Options, frame-ancestors) would refuse
// a frame from another port.
func appResponseHeaders(h http.Header, desktop string, tildeCookie func(string) bool) {
	if cookies := h.Values("Set-Cookie"); len(cookies) > 0 {
		h.Del("Set-Cookie")
		for _, line := range cookies {
			name, _, _ := strings.Cut(line, "=")
			if !tildeCookie(strings.TrimSpace(name)) {
				h.Add("Set-Cookie", line)
			}
		}
	}
	// Clearing the host's cookies would take Tilde's away too (and let a
	// page put another session's in their place); a service worker for the
	// whole app port would serve the user's other apps.
	h.Del("Clear-Site-Data")
	h.Del("Service-Worker-Allowed")
	h.Del("X-Frame-Options")
	if csps := h.Values("Content-Security-Policy"); len(csps) > 0 {
		h.Del("Content-Security-Policy")
		for _, line := range csps {
			// A header may carry several policies, comma-separated.
			for _, csp := range strings.Split(line, ",") {
				var keep []string
				for _, d := range strings.Split(csp, ";") {
					if f := strings.Fields(d); len(f) > 0 && !strings.EqualFold(f[0], "frame-ancestors") {
						keep = append(keep, strings.TrimSpace(d))
					}
				}
				if len(keep) > 0 {
					h.Add("Content-Security-Policy", strings.Join(keep, "; "))
				}
			}
		}
	}
	ancestors := "'none'"
	if desktop != "" {
		ancestors = desktop
	}
	h.Add("Content-Security-Policy", "frame-ancestors "+ancestors)
}

// appTransport is the user's connection pool to their apps, through the
// worker.
func (s *Server) appTransport(us *UserSession) http.RoundTripper {
	us.appsMu.Lock()
	defer us.appsMu.Unlock()
	if us.apps == nil {
		us.apps = &http.Transport{
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
				_, p, _ := net.SplitHostPort(addr)
				port, err := strconv.Atoi(p)
				if err != nil {
					return nil, err
				}
				return s.dialApp(ctx, us, port)
			},
			MaxIdleConnsPerHost: 8,
			IdleConnTimeout:     60 * time.Second,
			DisableCompression:  true,
		}
	}
	return us.apps
}

// dialWorkerApp opens a stream to the user's worker and has it connect to
// port (the worker's /internal/dial).
func dialWorkerApp(ctx context.Context, us *UserSession, port int) (net.Conn, error) {
	if us.conn == nil {
		return nil, errors.New("no worker")
	}
	stream, err := us.conn.Open()
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		stream.SetDeadline(dl)
	}
	fmt.Fprintf(stream, "GET /internal/dial?port=%d HTTP/1.1\r\nHost: worker\r\nConnection: Upgrade\r\nUpgrade: tilde-tcp\r\n%s: 1\r\n\r\n",
		port, protocol.InternalHeader)
	br := bufio.NewReader(stream)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		stream.Close()
		return nil, err
	}
	resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusSwitchingProtocols:
		stream.SetDeadline(time.Time{})
		return &bufferedConn{Conn: stream, r: br}, nil
	case http.StatusNotFound:
		stream.Close()
		return nil, errAppNotListening
	}
	stream.Close()
	return nil, fmt.Errorf("the worker answered %s", resp.Status)
}

// bufferedConn reads what the response reader already buffered first.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// appPage answers with a small page of Tilde's own on the app port.
func appPage(w http.ResponseWriter, status int, title, text string) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors *")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>%s</title>
<style>body{font:15px system-ui,sans-serif;background:#2b2b2f;color:#ddd;display:grid;place-items:center;height:90vh;margin:0}
div{max-width:28em;text-align:center}h1{font-size:1.3em;color:#fff}</style>
<div><h1>%s</h1><p>%s</p></div>`, html.EscapeString(title), html.EscapeString(title), html.EscapeString(text))
}
