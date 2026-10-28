package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestTokenStore(t *testing.T) {
	s := NewTokenStore(time.Hour)
	now := time.Now()
	s.now = func() time.Time { return now }

	a, _ := s.New("alice")
	b, _ := s.New("alice")
	c, _ := s.New("bob")
	if u, ok := s.Lookup(a); !ok || u.User != "alice" {
		t.Fatalf("Lookup(a) = %+v, %v", u, ok)
	}
	if _, ok := s.Lookup("not-a-token"); ok {
		t.Fatal("unknown token accepted")
	}
	s.Revoke(a)
	if _, ok := s.Lookup(a); ok {
		t.Fatal("revoked token accepted")
	}
	s.RevokeUser("alice")
	if _, ok := s.Lookup(b); ok {
		t.Fatal("RevokeUser left a token")
	}
	now = now.Add(2 * time.Hour)
	if _, ok := s.Lookup(c); ok {
		t.Fatal("expired token accepted")
	}
}

func TestLoginLimiter(t *testing.T) {
	l := newLoginLimiter(3, time.Minute)
	now := time.Now()
	l.now = func() time.Time { return now }
	allowed := func(addr, user string) bool {
		ok, _ := l.Begin(addr, user)
		return ok
	}
	// Attempts count when they start, before PAM answers.
	for i := 0; i < 3; i++ {
		if !allowed("1.2.3.4", "alice") {
			t.Fatalf("blocked after %d attempts", i)
		}
	}
	if ok, wait := l.Begin("1.2.3.4", "alice"); ok || wait <= 0 {
		t.Fatal("not blocked after 3 attempts")
	}
	if !allowed("5.6.7.8", "alice") {
		t.Fatal("other address blocked")
	}

	// A right password takes back only that user's attempts.
	l.Succeed("1.2.3.4", "mallory")
	if allowed("1.2.3.4", "alice") {
		t.Fatal("another account's login reset the count")
	}
	l.Succeed("1.2.3.4", "alice")
	for i := 0; i < 3; i++ {
		if !allowed("1.2.3.4", "bob") {
			t.Fatal("blocked after alice's success")
		}
	}
	// Cancel takes back one attempt that ended without a verdict.
	l.Cancel("1.2.3.4", "bob")
	if !allowed("1.2.3.4", "bob") || allowed("1.2.3.4", "bob") {
		t.Fatal("Cancel did not free exactly one attempt")
	}

	now = now.Add(2 * time.Minute)
	if !allowed("1.2.3.4", "alice") {
		t.Fatal("still blocked after the window")
	}
}

func TestOriginCheck(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := originCheck([]string{"https://tilde.example.com"})(ok)
	tests := []struct {
		name, method, origin, upgrade string
		want                          int
	}{
		{"plain GET needs no origin", http.MethodGet, "", "", http.StatusNoContent},
		{"POST same origin", http.MethodPost, "https://desk.lan:8443", "", http.StatusNoContent},
		{"POST without origin", http.MethodPost, "", "", http.StatusForbidden},
		{"POST cross origin", http.MethodPost, "https://evil.example", "", http.StatusForbidden},
		{"POST allowed extra origin", http.MethodPost, "https://tilde.example.com", "", http.StatusNoContent},
		{"websocket cross origin", http.MethodGet, "https://evil.example", "websocket", http.StatusForbidden},
		{"websocket same origin", http.MethodGet, "https://desk.lan:8443", "websocket", http.StatusNoContent},
		{"null origin", http.MethodPost, "null", "", http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(tt.method, "https://desk.lan:8443/api/v1/fs/mkdir", nil)
			if tt.origin != "" {
				r.Header.Set("Origin", tt.origin)
			}
			if tt.upgrade != "" {
				r.Header.Set("Upgrade", tt.upgrade)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tt.want {
				t.Fatalf("status %d, want %d", w.Code, tt.want)
			}
		})
	}
}

func TestAPIRequiresLogin(t *testing.T) {
	s, err := New(Config{Listen: "127.0.0.1:0", Web: fstest.MapFS{"index.html": {Data: []byte("app")}}})
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	for _, path := range []string{"/api/v1/hello", "/api/auth/session"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s: status %d, want 401", path, w.Code)
		}
	}
	// Unknown non-API paths serve the app shell, with security headers.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/some/route", nil))
	if w.Code != http.StatusOK || w.Body.String() != "app" {
		t.Fatalf("SPA fallback: %d %q", w.Code, w.Body.String())
	}
	if w.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("missing CSP")
	}
}

// handlerTransport answers proxied requests with a handler, standing in
// for the yamux stream to a worker.
type handlerTransport struct{ h http.Handler }

func (t handlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	t.h.ServeHTTP(w, r)
	return w.Result(), nil
}

// TestWorkerResponses: the worker is the user's process, so its responses
// cannot set the server's cookies, and are not cached unless it says so.
func TestWorkerResponses(t *testing.T) {
	worker := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "__Host-tilde", Value: "planted", Path: "/"})
		if r.URL.Path == "/api/v1/fs/thumbnail" {
			w.Header().Set("Cache-Control", "private, max-age=86400")
		}
		w.Write([]byte("{}"))
	})
	lt := newLockTest(t, newWorkerProxy(handlerTransport{worker}))
	tok := lt.login("alice")
	for path, cache := range map[string]string{"/api/v1/fs/list": "no-store", "/api/v1/fs/thumbnail": "private, max-age=86400"} {
		w := lt.do(http.MethodGet, path, tok, "")
		if w.Code != http.StatusOK || len(w.Result().Cookies()) != 0 || w.Header().Get("Cache-Control") != cache {
			t.Errorf("%s: %d, cookies %v, Cache-Control %q", path, w.Code, w.Result().Cookies(), w.Header().Get("Cache-Control"))
		}
	}
}

// TestSecurityHeaders: HSTS only with the administrator's own certificate,
// where a browser can never be locked out by a self-signed one changing.
func TestSecurityHeaders(t *testing.T) {
	for _, c := range []struct {
		cfg  Config
		hsts bool
	}{
		{Config{Listen: "127.0.0.1:0"}, false},
		{Config{Listen: "127.0.0.1:0", TLSCert: "/etc/tilde/cert.pem"}, true},
		{Config{Listen: "127.0.0.1:0", TLSCert: "/etc/tilde/cert.pem", Dev: true}, false},
	} {
		s, err := New(c.cfg)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/auth/session", nil))
		h := w.Header()
		if h.Get("Cross-Origin-Resource-Policy") != "same-origin" || h.Get("X-Content-Type-Options") != "nosniff" ||
			!strings.Contains(h.Get("Content-Security-Policy"), "script-src 'self';") {
			t.Errorf("%+v: headers %v", c.cfg, h)
		}
		if got := h.Get("Strict-Transport-Security") != ""; got != c.hsts {
			t.Errorf("%+v: HSTS %v, want %v", c.cfg, got, c.hsts)
		}
	}
}

// TestClientAddr: behind a trusted proxy, the address the proxy appended
// counts, not the ones the client wrote itself.
func TestClientAddr(t *testing.T) {
	for _, c := range []struct {
		xff   []string
		trust bool
		want  string
	}{
		{nil, true, "198.51.100.1"},
		{[]string{"203.0.113.9"}, false, "198.51.100.1"},
		{[]string{"203.0.113.9"}, true, "203.0.113.9"},
		{[]string{"10.9.9.9, 203.0.113.9"}, true, "203.0.113.9"},
		{[]string{"10.9.9.9", "203.0.113.9"}, true, "203.0.113.9"},
		{[]string{"10.9.9.9, "}, true, "198.51.100.1"},
	} {
		r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
		r.RemoteAddr = "198.51.100.1:4000"
		for _, v := range c.xff {
			r.Header.Add("X-Forwarded-For", v)
		}
		if got := clientAddr(r, c.trust); got != c.want {
			t.Errorf("%q trust=%v: %s, want %s", c.xff, c.trust, got, c.want)
		}
	}
}

// TestStaticFont: the bundled font (D56) is served as a font, cached like
// the other hashed assets, and allowed by the CSP.
func TestStaticFont(t *testing.T) {
	web := fstest.MapFS{
		"index.html":                   {Data: []byte("app")},
		"assets/tilde-ui-abc123.woff2": {Data: []byte("wOF2font")},
	}
	s, err := New(Config{Listen: "127.0.0.1:0", Web: web})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/assets/tilde-ui-abc123.woff2", nil))
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "font/woff2" {
		t.Fatalf("status %d, type %q", w.Code, w.Header().Get("Content-Type"))
	}
	if !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("cache %q", w.Header().Get("Cache-Control"))
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "font-src 'self'") {
		t.Fatalf("csp %q", w.Header().Get("Content-Security-Policy"))
	}
}
