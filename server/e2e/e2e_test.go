//go:build e2e

// Package e2e drives a running Tilde server with a real account. It is the
// "real-privilege test harness": run it inside a disposable VM (see
// docs/development.md), never against a machine you care about.
//
//	sudo ./bin/tilde serve --dev --listen 127.0.0.1:8443 &
//	TILDE_URL=http://127.0.0.1:8443 TILDE_USER=tester TILDE_PASS=secret \
//	  go test -tags e2e ./e2e/ -v
package e2e

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type env struct {
	base   *url.URL
	client *http.Client
	user   string
}

func setup(t *testing.T) *env {
	t.Helper()
	raw, user, pass := os.Getenv("TILDE_URL"), os.Getenv("TILDE_USER"), os.Getenv("TILDE_PASS")
	if raw == "" || user == "" || pass == "" {
		t.Skip("set TILDE_URL, TILDE_USER and TILDE_PASS")
	}
	base, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	e := &env{
		base: base,
		user: user,
		client: &http.Client{
			Jar:     jar,
			Timeout: 30 * time.Second,
			// Self-signed certificates are expected in a test VM.
			Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		},
	}
	resp := e.do(t, http.MethodPost, "/api/auth/login", map[string]string{"username": user, "password": pass})
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("login: %d %s", resp.StatusCode, body)
	}
	resp.Body.Close()
	return e
}

func (e *env) do(t *testing.T, method, path string, body any) *http.Response {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.base.String()+path, r)
	req.Header.Set("Origin", e.base.Scheme+"://"+e.base.Host)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func decode(t *testing.T, resp *http.Response, want int, v any) {
	t.Helper()
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("%s: status %d, want %d: %s", resp.Request.URL.Path, resp.StatusCode, want, body)
	}
	if v != nil {
		if err := json.Unmarshal(body, v); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWorkerRunsAsTheUser(t *testing.T) {
	e := setup(t)
	var hello struct {
		User string `json:"user"`
		UID  int    `json:"uid"`
		Home string `json:"home"`
	}
	decode(t, e.do(t, http.MethodGet, "/api/v1/hello", nil), http.StatusOK, &hello)
	if hello.User != e.user || hello.UID == 0 {
		t.Fatalf("hello = %+v", hello)
	}

	// The kernel, not Tilde, decides: a normal user cannot list /root.
	resp := e.do(t, http.MethodGet, "/api/v1/fs/list?path=/root", nil)
	decode(t, resp, http.StatusForbidden, nil)

	// The terminal's shell runs as the user too.
	var term struct {
		ID string `json:"id"`
	}
	decode(t, e.do(t, http.MethodPost, "/api/v1/terminals", map[string]int{"cols": 80, "rows": 24}), http.StatusCreated, &term)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	wsURL := *e.base
	wsURL.Scheme = strings.Replace(wsURL.Scheme, "http", "ws", 1)
	wsURL.Path = "/api/v1/terminals/" + term.ID + "/attach"
	h := http.Header{}
	h.Set("Origin", e.base.Scheme+"://"+e.base.Host)
	conn, _, err := websocket.Dial(ctx, wsURL.String(), &websocket.DialOptions{HTTPClient: e.client, HTTPHeader: h})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	conn.Write(ctx, websocket.MessageBinary, []byte("echo uid=$(id -u) user=$(whoami)\n"))
	var out strings.Builder
	for !strings.Contains(out.String(), "user="+e.user) {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("terminal output so far %q: %v", out.String(), err)
		}
		out.Write(data)
	}
	if strings.Contains(out.String(), "uid=0 ") {
		t.Fatal("shell is running as root")
	}
}

func TestCrossOriginRefused(t *testing.T) {
	e := setup(t)
	req, _ := http.NewRequest(http.MethodPost, e.base.String()+"/api/v1/fs/mkdir", strings.NewReader(`{"path":"/tmp/x"}`))
	req.Header.Set("Origin", "https://evil.example")
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	decode(t, resp, http.StatusForbidden, nil)
}

func TestLogoutEndsSession(t *testing.T) {
	e := setup(t)
	decode(t, e.do(t, http.MethodPost, "/api/auth/logout", nil), http.StatusNoContent, nil)
	decode(t, e.do(t, http.MethodGet, "/api/v1/hello", nil), http.StatusUnauthorized, nil)
}
