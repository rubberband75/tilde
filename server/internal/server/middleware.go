package server

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
)

type apiError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func jsonDecode(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// contentSecurityPolicy for the desktop itself. 'unsafe-inline' for styles is
// needed because xterm.js and Vue transitions set inline styles; scripts stay
// strictly same-origin. A cross-site scripting bug in Tilde is a shell, so
// do not loosen script-src.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; " +
	"media-src 'self' blob:; " +
	"font-src 'self' data:; " +
	"connect-src 'self'; " +
	"frame-src 'self'; " +
	"frame-ancestors 'self'; " +
	"base-uri 'none'; " +
	"form-action 'self'; " +
	"object-src 'none'"

// securityHeaders sets headers on every response. Responses proxied from
// the worker may add a stricter CSP of their own (file downloads add
// "sandbox"); browsers enforce both.
//
// hsts is set only with a certificate the administrator configured (D45):
// on a self-signed one it would turn the browser's warning into an error
// nobody can click through once the certificate changes.
//
// apps, when local web apps are on, names the app port's origin (D76):
// windows frame it, and post the handoff form to it.
func securityHeaders(hsts bool, apps func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			csp := contentSecurityPolicy
			if apps != nil {
				o := apps(r)
				csp = strings.Replace(csp, "frame-src 'self'", "frame-src 'self' "+o, 1)
				csp = strings.Replace(csp, "form-action 'self'", "form-action 'self' "+o, 1)
			}
			h.Set("Content-Security-Policy", csp)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "SAMEORIGIN")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			// Other sites cannot load Tilde's responses (file contents,
			// thumbnails) into their pages, even without a cookie.
			h.Set("Cross-Origin-Resource-Policy", "same-origin")
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			if hsts {
				h.Set("Strict-Transport-Security", "max-age=31536000")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// originCheck rejects cross-site requests that could act with the user's
// cookie: every state-changing method and every WebSocket upgrade must carry
// an Origin header naming this host. This blocks cross-site WebSocket
// hijacking, the hole webdesktopd left open. SameSite=Strict cookies are a
// second layer.
func originCheck(extra []string) func(http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, o := range extra {
		if u, err := url.Parse(o); err == nil && u.Host != "" {
			allowed[strings.ToLower(u.Host)] = true
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if needsOriginCheck(r) && !originAllowed(r, allowed) {
				writeJSON(w, http.StatusForbidden, apiError{Error: "cross-origin request refused", Code: "bad_origin"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// fetchMetadataCheck turns away API requests that browsers say come from
// another origin of the same site, or another site. Cookies are per host,
// not per port, and SameSite counts every port of the host as one site,
// so the app port's pages (D76) would otherwise reach the API with the
// user's cookie, by GET (which the Origin check leaves alone) or a form.
// Browsers too old to send Sec-Fetch-Site are left to the other checks.
func fetchMetadataCheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			switch r.Header.Get("Sec-Fetch-Site") {
			case "same-site", "cross-site":
				writeJSON(w, http.StatusForbidden, apiError{Error: "cross-origin request refused", Code: "bad_origin"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func needsOriginCheck(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
	}
	return true
}

func originAllowed(r *http.Request, allowed map[string]bool) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || origin == "null" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Host)
	return host == strings.ToLower(r.Host) || allowed[host]
}

// clientAddr returns the remote IP. Tilde is meant to be reached directly or
// over a VPN, so X-Forwarded-For is ignored unless trustProxy is set. Then
// the last address counts: the proxy appends the one it saw, and anything
// before it came from the client, which could otherwise send a new
// address with every guess to escape the rate limit.
func clientAddr(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Values("X-Forwarded-For"); len(xff) > 0 {
			last := xff[len(xff)-1]
			if i := strings.LastIndexByte(last, ','); i >= 0 {
				last = last[i+1:]
			}
			if last = strings.TrimSpace(last); last != "" {
				return last
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
