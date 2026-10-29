package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// appsTest is a server with alice logged in and a fake app of hers on
// port 4242.
type appsTest struct {
	*lockTest
	apps http.Handler
	tok  string
	// seen is what the app got last.
	seen *http.Request
}

func newAppsTest(t *testing.T) *appsTest {
	t.Helper()
	at := &appsTest{lockTest: newLockTest(t, http.NotFoundHandler())}
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		at.seen = r
		w.Header().Add("Set-Cookie", "__Host-tilde=forged; Path=/; Secure")
		w.Header().Add("Set-Cookie", "lab=1; Path=/")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'self'")
		w.Header().Set("Clear-Site-Data", `"cookies"`)
		w.Header().Set("Service-Worker-Allowed", "/")
		fmt.Fprint(w, "app page")
	}))
	t.Cleanup(app.Close)
	at.s.dialApp = func(ctx context.Context, _ *UserSession, port int) (net.Conn, error) {
		if port != 4242 {
			return nil, errAppNotListening
		}
		var d net.Dialer
		return d.DialContext(ctx, "tcp", app.Listener.Addr().String())
	}
	at.apps = at.s.appsHandler()
	at.tok = at.login("alice")
	return at
}

func (at *appsTest) handoff() (code, origin string) {
	at.t.Helper()
	w := at.do(http.MethodPost, "/api/auth/apps/handoff", at.tok, "")
	at.expect(w, http.StatusOK, "")
	var res struct{ Code, Origin string }
	json.Unmarshal(w.Body.Bytes(), &res)
	return res.Code, res.Origin
}

// enter posts a handoff form to the app port, as the desktop does; origin
// may carry Sec-Fetch-Site after a space ("null same-site").
func (at *appsTest) enter(code, next, origin string) *httptest.ResponseRecorder {
	form := url.Values{"code": {code}, "next": {next}}
	req := httptest.NewRequest(http.MethodPost, "https://desk.lan:444/_tilde/enter", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	origin, site, _ := strings.Cut(origin, " ")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if site != "" {
		req.Header.Set("Sec-Fetch-Site", site)
	}
	// On Tilde's host, the browser sends Tilde's cookie along.
	req.AddCookie(&http.Cookie{Name: at.s.cookie, Value: at.tok})
	w := httptest.NewRecorder()
	at.apps.ServeHTTP(w, req)
	return w
}

func (at *appsTest) get(path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "https://desk.lan:444"+path, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	at.apps.ServeHTTP(w, req)
	return w
}

// TestAppsHandoff: the desktop's code, posted from the desktop's origin,
// once, sets the app cookie and goes on to the app; nothing else does.
func TestAppsHandoff(t *testing.T) {
	at := newAppsTest(t)
	code, origin := at.handoff()
	if origin != "https://desk.lan:444" {
		t.Fatalf("apps origin %q", origin)
	}
	// From elsewhere, or without saying where from: refused, and the code
	// is spent.
	if w := at.enter(code, "/proxy/4242/", "https://evil.example"); w.Code != http.StatusForbidden {
		t.Fatalf("other origin: %d", w.Code)
	}
	if w := at.enter(code, "/proxy/4242/", "http://desk.lan"); w.Code != http.StatusUnauthorized {
		t.Fatalf("spent code: %d", w.Code)
	}
	// Without a Referer, browsers send "Origin: null"; then only a form
	// from this site will do.
	for _, o := range []string{"null cross-site", "null", ""} {
		code, _ = at.handoff()
		if w := at.enter(code, "/proxy/4242/", o); w.Code != http.StatusForbidden {
			t.Fatalf("origin %q: %d", o, w.Code)
		}
	}
	code, _ = at.handoff()
	if w := at.enter(code, "/proxy/4242/", "null same-site"); w.Code != http.StatusSeeOther {
		t.Fatalf("from the desktop without a Referer: %d", w.Code)
	}
	// A code given to another browser (here alice's other one), posted
	// into this one from a page elsewhere on the site: refused.
	mine := at.tok
	at.tok = at.login("alice")
	theirs, _ := at.handoff()
	at.tok = mine
	if w := at.enter(theirs, "/proxy/4242/", "null same-site"); w.Code != http.StatusForbidden {
		t.Fatalf("another browser's code: %d", w.Code)
	}
	// One browser cannot hold more than a few codes at once.
	for i := range maxHandoffsToken {
		if w := at.do(http.MethodPost, "/api/auth/apps/handoff", at.tok, ""); w.Code != http.StatusOK && i < maxHandoffsToken-1 {
			t.Fatalf("handoff %d: %d", i, w.Code)
		}
	}
	at.expect(at.do(http.MethodPost, "/api/auth/apps/handoff", at.tok, ""), http.StatusTooManyRequests, "busy")
	at.s.apps.codes = map[string]appHandoff{}
	code, _ = at.handoff()
	if w := at.enter(code, "//evil.example/", "http://desk.lan"); w.Code != http.StatusBadRequest {
		t.Fatalf("next elsewhere: %d", w.Code)
	}
	code, _ = at.handoff()
	w := at.enter(code, "/proxy/4242/notes?x=1", "http://desk.lan")
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/proxy/4242/notes?x=1" {
		t.Fatalf("enter: %d %v", w.Code, w.Header())
	}
	c := w.Result().Cookies()
	if len(c) != 2 || c[0].Name != at.s.cookie+"-app" || !c[0].HttpOnly || c[0].SameSite != http.SameSiteStrictMode ||
		c[1].Name != at.s.cookie+"-app-owner" || w.Header().Get("Clear-Site-Data") != "" {
		t.Fatalf("cookies %+v", c)
	}
	// Another user's apps were used in this browser: their storage goes.
	code, _ = at.handoff()
	form := url.Values{"code": {code}, "next": {"/proxy/4242/"}}
	req := httptest.NewRequest(http.MethodPost, "https://desk.lan:444/_tilde/enter", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://desk.lan")
	req.AddCookie(&http.Cookie{Name: at.s.cookie + "-app-owner", Value: "someone-else"})
	w = httptest.NewRecorder()
	at.apps.ServeHTTP(w, req)
	if w.Code != http.StatusSeeOther || w.Header().Get("Clear-Site-Data") != `"storage", "cache"` {
		t.Fatalf("after another user: %d %v", w.Code, w.Header())
	}

	// Locked or logged out: no code.
	at.expect(at.do(http.MethodPost, "/api/auth/lock", at.tok, ""), http.StatusNoContent, "")
	at.expect(at.do(http.MethodPost, "/api/auth/apps/handoff", at.tok, ""), http.StatusLocked, "locked")
	at.expect(at.do(http.MethodPost, "/api/auth/apps/handoff", "", ""), http.StatusUnauthorized, "not_logged_in")
}

// TestAppsProxy: the app gets its request without Tilde's cookies, and
// cannot set them; the desktop may frame it; a lock or logout ends it.
func TestAppsProxy(t *testing.T) {
	at := newAppsTest(t)
	code, _ := at.handoff()
	app := at.enter(code, "/proxy/4242/", "http://desk.lan").Result().Cookies()[0]
	main := &http.Cookie{Name: at.s.cookie, Value: at.tok}
	lab := &http.Cookie{Name: "lab", Value: "7"}

	w := at.get("/proxy/4242/notes/a%2Fb?x=1", app, main, lab, &http.Cookie{Name: "q", Value: `"a b"`, Quoted: false})
	if w.Code != http.StatusOK || w.Body.String() != "app page" {
		t.Fatalf("proxy: %d %s", w.Code, w.Body)
	}
	r := at.seen
	if r.URL.EscapedPath() != "/notes/a%2Fb" || r.URL.RawQuery != "x=1" || r.Host != "desk.lan:444" ||
		r.Header.Get("Cookie") != `lab=7; q="a b"` || r.Header.Get("X-Forwarded-Prefix") != "/proxy/4242" {
		t.Fatalf("the app got %s %s host=%s cookie=%q prefix=%q", r.Method, r.URL, r.Host, r.Header.Get("Cookie"), r.Header.Get("X-Forwarded-Prefix"))
	}
	h := w.Header()
	if got := h.Values("Set-Cookie"); len(got) != 1 || !strings.HasPrefix(got[0], "lab=1") {
		t.Fatalf("Set-Cookie %q", got)
	}
	if h.Get("X-Frame-Options") != "" || strings.Join(h.Values("Content-Security-Policy"), " | ") != "default-src 'self' | frame-ancestors http://desk.lan" ||
		h.Get("Clear-Site-Data") != "" || h.Get("Service-Worker-Allowed") != "" {
		t.Fatalf("headers %v", h)
	}
	// Policies joined by commas lose their frame-ancestors too.
	hh := http.Header{"Content-Security-Policy": {"script-src 'self', frame-ancestors 'none'; img-src *"}}
	appResponseHeaders(hh, "http://desk.lan", at.s.tildeCookie)
	if got := strings.Join(hh.Values("Content-Security-Policy"), " | "); got != "script-src 'self' | img-src * | frame-ancestors http://desk.lan" {
		t.Fatalf("comma policies: %s", got)
	}
	// Other pages: another site's, or this site's doing more than opening it.
	for site, mode := range map[string]string{"cross-site": "navigate", "same-site": "cors"} {
		req := httptest.NewRequest(http.MethodGet, "https://desk.lan:444/proxy/4242/", nil)
		req.AddCookie(app)
		req.Header.Set("Sec-Fetch-Site", site)
		req.Header.Set("Sec-Fetch-Mode", mode)
		w := httptest.NewRecorder()
		at.apps.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s: %d", site, mode, w.Code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "https://desk.lan:444/absproxy/4242/lab/tree", nil)
	req.AddCookie(app)
	req.Header.Set("X-Forwarded-Prefix", "/forged")
	w = httptest.NewRecorder()
	at.apps.ServeHTTP(w, req)
	if w.Code != http.StatusOK || at.seen.URL.Path != "/absproxy/4242/lab/tree" || at.seen.Header.Get("X-Forwarded-Prefix") != "" {
		t.Fatalf("absproxy: %d %s %q", w.Code, at.seen.URL.Path, at.seen.Header.Get("X-Forwarded-Prefix"))
	}

	// Tilde's own cookie is not the app port's; nothing listening; not a port.
	for path, want := range map[string]int{"/proxy/4242/": http.StatusUnauthorized, "/proxy/99/": http.StatusBadGateway, "/proxy/x/": http.StatusNotFound} {
		cookies := []*http.Cookie{app}
		if want == http.StatusUnauthorized {
			cookies = []*http.Cookie{main}
		}
		if w := at.get(path, cookies...); w.Code != want {
			t.Errorf("%s: %d, want %d", path, w.Code, want)
		}
	}

	at.expect(at.do(http.MethodPost, "/api/auth/lock", at.tok, ""), http.StatusNoContent, "")
	if w := at.get("/proxy/4242/", app); w.Code != http.StatusLocked {
		t.Fatalf("locked: %d", w.Code)
	}
	at.s.tokens.RevokeUser("alice")
	if w := at.get("/proxy/4242/", app); w.Code != http.StatusUnauthorized {
		t.Fatalf("logged out: %d", w.Code)
	}
	at.s.apps.sweep(at.now, at.s.tokens.ValidHash)
	if len(at.s.apps.tokens) != 0 {
		t.Fatal("the app cookie outlived its token")
	}
}

// TestFetchMetadata: the app port's pages (same site, another origin)
// cannot use the API with the user's cookie; Tilde's own requests can.
func TestFetchMetadata(t *testing.T) {
	at := newAppsTest(t)
	for site, want := range map[string]int{"same-site": http.StatusForbidden, "cross-site": http.StatusForbidden, "same-origin": http.StatusNotFound, "": http.StatusNotFound} {
		req := httptest.NewRequest(http.MethodGet, "http://desk.lan/api/v1/hello", nil)
		req.AddCookie(&http.Cookie{Name: at.s.cookie, Value: at.tok})
		if site != "" {
			req.Header.Set("Sec-Fetch-Site", site)
		}
		w := httptest.NewRecorder()
		at.h.ServeHTTP(w, req)
		if w.Code != want {
			t.Errorf("Sec-Fetch-Site %q: %d, want %d", site, w.Code, want)
		}
	}
	// A tab says whose desktop it is: a cookie swapped for another
	// account's session gets nothing through.
	for _, c := range []struct {
		header, query string
		want          int
	}{{"bob", "", http.StatusUnauthorized}, {"", "bob", http.StatusUnauthorized}, {"alice", "alice", http.StatusNotFound}} {
		req := httptest.NewRequest(http.MethodGet, "http://desk.lan/api/v1/hello?tilde-user="+c.query, nil)
		req.AddCookie(&http.Cookie{Name: at.s.cookie, Value: at.tok})
		if c.header != "" {
			req.Header.Set("X-Tilde-User", c.header)
		}
		w := httptest.NewRecorder()
		at.h.ServeHTTP(w, req)
		if w.Code != c.want || (c.want == http.StatusUnauthorized && !strings.Contains(w.Body.String(), "session_changed")) {
			t.Errorf("%+v: %d %s", c, w.Code, w.Body)
		}
	}
	w := at.do(http.MethodGet, "/api/auth/session", at.tok, "")
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-src 'self' https://desk.lan:444;") ||
		!strings.Contains(csp, "form-action 'self' https://desk.lan:444;") {
		t.Fatalf("CSP %s", csp)
	}
}

func TestAppsAddress(t *testing.T) {
	for _, c := range []struct {
		listen, apps, want string
		offset             int
		bad                bool
	}{
		{"127.0.0.1:8443", "", "127.0.0.1:8444", 1, false},
		{"[::1]:8443", "", "[::1]:8444", 1, false},
		{"0.0.0.0:8443", "0.0.0.0:9000", "0.0.0.0:9000", 557, false},
		{"127.0.0.1:8443", "127.0.0.1:8443", "", 0, true},
		{"127.0.0.1:8443", "nonsense", "", 0, true},
	} {
		got, off, err := appsAddress(c.listen, c.apps)
		if (err != nil) != c.bad || got != c.want || (!c.bad && off != c.offset) {
			t.Errorf("appsAddress(%q, %q) = %q %d %v", c.listen, c.apps, got, off, err)
		}
	}
}
