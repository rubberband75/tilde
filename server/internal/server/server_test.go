package server

import (
	"net/http"
	"net/http/httptest"
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
	if u, ok := s.Lookup(a); !ok || u != "alice" {
		t.Fatalf("Lookup(a) = %q, %v", u, ok)
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
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allowed("1.2.3.4"); !ok {
			t.Fatalf("blocked after %d failures", i)
		}
		l.Fail("1.2.3.4")
	}
	if ok, wait := l.Allowed("1.2.3.4"); ok || wait <= 0 {
		t.Fatal("not blocked after 3 failures")
	}
	if ok, _ := l.Allowed("5.6.7.8"); !ok {
		t.Fatal("other address blocked")
	}
	now = now.Add(2 * time.Minute)
	if ok, _ := l.Allowed("1.2.3.4"); !ok {
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
