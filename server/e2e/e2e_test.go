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
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
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
	out := e.shell(t, "echo uid=$(id -u) user=$(whoami)")
	if !strings.Contains(out, "user="+e.user) || strings.Contains(out, "uid=0 ") {
		t.Fatalf("shell output %q", out)
	}
}

// shell runs script in a new terminal and returns what it printed. The
// output is fenced by markers that the terminal's echo of the typed
// command cannot match ("BEGIN""MARK" types differently than it prints).
func (e *env) shell(t *testing.T, script string) string {
	t.Helper()
	var term struct {
		ID string `json:"id"`
	}
	decode(t, e.do(t, http.MethodPost, "/api/v1/terminals", map[string]int{"cols": 200, "rows": 24}), http.StatusCreated, &term)

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
	line := "echo \"BEGIN\"\"MARK\"; " + script + "; echo \"END\"\"MARK\"\n"
	if err := conn.Write(ctx, websocket.MessageBinary, []byte(line)); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("terminal output so far %q: %v", out.String(), err)
		}
		out.Write(data)
		all := out.String()
		if i := strings.LastIndex(all, "BEGINMARK"); i >= 0 {
			if j := strings.Index(all[i:], "ENDMARK"); j >= 0 {
				return strings.ReplaceAll(all[i+len("BEGINMARK"):i+j], "\r", "")
			}
		}
	}
}

// fields parses "key=value" words from shell output.
func fields(out string) map[string]string {
	m := map[string]string{}
	for _, w := range strings.Fields(out) {
		if k, v, ok := strings.Cut(w, "="); ok {
			m[k] = v
		}
	}
	return m
}

// TestLogindSession checks what the session helper exists for (D9): the
// shell runs inside a logind session of type "web" opened through PAM, with
// a runtime directory and a session D-Bus.
func TestLogindSession(t *testing.T) {
	e := setup(t)
	out := e.shell(t, `echo type=$(loginctl show-session "$XDG_SESSION_ID" -p Type --value) `+
		`class=$(loginctl show-session "$XDG_SESSION_ID" -p Class --value) `+
		`user=$(loginctl show-session "$XDG_SESSION_ID" -p Name --value) `+
		`runtime=$(test -d "$XDG_RUNTIME_DIR" && test -O "$XDG_RUNTIME_DIR" && echo ok) `+
		`bus=$(busctl --user status >/dev/null 2>&1 && echo ok)`)
	got := fields(out)
	want := map[string]string{"type": "web", "class": "user", "user": e.user, "runtime": "ok", "bus": "ok"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q (shell output %q)", k, got[k], v, out)
		}
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

// TestLogoutClosesLogindSession checks that logging out closes the logind
// session while nohup jobs survive, as on Mint (D15). It reads logind on
// this machine, so it only runs against a server on loopback.
func TestLogoutClosesLogindSession(t *testing.T) {
	e := setup(t)
	if ip := net.ParseIP(e.base.Hostname()); e.base.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		t.Skip("needs the server on this machine")
	}
	// A unique duration, so pgrep finds this job; it ends on its own.
	marker := fmt.Sprintf("300.%06d", time.Now().UnixNano()%1000000)
	out := e.shell(t, "nohup sleep "+marker+" >/dev/null 2>&1 & echo session=$XDG_SESSION_ID")
	id := fields(out)["session"]
	if id == "" {
		t.Fatalf("no XDG_SESSION_ID in %q", out)
	}
	if state := sessionState(id); state != "active" && state != "online" {
		t.Fatalf("session %s state %q before logout", id, state)
	}

	decode(t, e.do(t, http.MethodPost, "/api/auth/logout", nil), http.StatusNoContent, nil)

	// With a nohup job still running, logind keeps the session as
	// "closing" (KillUserProcesses=no, the Ubuntu and Mint default).
	deadline := time.Now().Add(10 * time.Second)
	for {
		state := sessionState(id)
		if state == "" || state == "closing" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("session %s still %q after logout", id, state)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err := exec.Command("pgrep", "-u", e.user, "-f", "sleep "+marker).Run(); err != nil {
		t.Errorf("the nohup job did not survive logout: %v", err)
	}
}

// sessionState returns logind's State for a session, or "" if it is gone.
func sessionState(id string) string {
	out, err := exec.Command("loginctl", "show-session", id, "-p", "State", "--value").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// TestRepeatedLogins opens and closes many PAM sessions. pam_loginuid only
// works from the helper's main thread; when PAM ran on another Go thread,
// about one login in twenty failed with "could not open a login session".
func TestRepeatedLogins(t *testing.T) {
	for i := 0; i < 25; i++ {
		e := setup(t)
		decode(t, e.do(t, http.MethodPost, "/api/auth/logout", nil), http.StatusNoContent, nil)
	}
}

// TestJobsRunAsTheUser checks that file jobs get the kernel's answer for the
// user: they can neither read /etc/shadow nor delete files in /etc.
func TestJobsRunAsTheUser(t *testing.T) {
	e := setup(t)
	// Logging out ends the session and its finished jobs with it.
	t.Cleanup(func() { e.do(t, http.MethodPost, "/api/auth/logout", nil).Body.Close() })
	var hello struct {
		Home string `json:"home"`
	}
	decode(t, e.do(t, http.MethodGet, "/api/v1/hello", nil), http.StatusOK, &hello)

	type job struct {
		ID         string `json:"id"`
		State      string `json:"state"`
		ErrorCount int    `json:"errorCount"`
		Errors     []struct {
			Code string `json:"code"`
		} `json:"errors"`
		Finished *time.Time `json:"finished"`
	}
	run := func(body map[string]any) job {
		var j job
		decode(t, e.do(t, http.MethodPost, "/api/v1/jobs", body), http.StatusAccepted, &j)
		deadline := time.Now().Add(15 * time.Second)
		for j.Finished == nil {
			if time.Now().After(deadline) {
				t.Fatalf("job %s did not finish", j.ID)
			}
			time.Sleep(50 * time.Millisecond)
			decode(t, e.do(t, http.MethodGet, "/api/v1/jobs/"+j.ID, nil), http.StatusOK, &j)
		}
		return j
	}
	for name, body := range map[string]map[string]any{
		"copy /etc/shadow":     {"op": "copy", "sources": []string{"/etc/shadow"}, "dest": hello.Home},
		"delete /etc/hostname": {"op": "delete", "sources": []string{"/etc/hostname"}},
	} {
		j := run(body)
		if j.State != "done" || j.ErrorCount != 1 || j.Errors[0].Code != "permission_denied" {
			t.Errorf("%s: %+v", name, j)
		}
	}
}

// TestDrivesFollowUdisks checks the worker's udisks client on a real system
// bus: the stick the VM script mounted for the test user is listed, mounts
// and unmounts made outside Tilde arrive as "drives" events, and Eject
// unmounts it although polkit refuses a web session the power-off.
func TestDrivesFollowUdisks(t *testing.T) {
	label := os.Getenv("TILDE_USB")
	if label == "" {
		t.Skip("set TILDE_USB to the label of a volume the test user mounted through udisks")
	}
	e := setup(t)
	type volume struct {
		ID, Name, Device, MountPoint string
		CanEject                     bool
	}
	find := func() volume {
		var res struct {
			Udisks  bool
			Volumes []volume
		}
		decode(t, e.do(t, http.MethodGet, "/api/v1/drives", nil), http.StatusOK, &res)
		if !res.Udisks {
			t.Fatal("the worker cannot reach udisks")
		}
		for _, v := range res.Volumes {
			if v.Name == label {
				return v
			}
		}
		t.Fatalf("%s not listed: %+v", label, res.Volumes)
		return volume{}
	}
	stick := find()
	if stick.MountPoint != "/media/"+e.user+"/"+label || !stick.CanEject {
		t.Fatalf("stick = %+v", stick)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	wsURL := *e.base
	wsURL.Scheme = strings.Replace(wsURL.Scheme, "http", "ws", 1)
	wsURL.Path = "/api/v1/events"
	h := http.Header{}
	h.Set("Origin", e.base.Scheme+"://"+e.base.Host)
	conn, _, err := websocket.Dial(ctx, wsURL.String(), &websocket.DialOptions{HTTPClient: e.client, HTTPHeader: h})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	waitDrives := func() {
		t.Helper()
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				t.Fatalf("no drives event: %v", err)
			}
			if strings.Contains(string(data), `"type":"drives"`) {
				return
			}
		}
	}
	udisksctl := func(args ...string) {
		t.Helper()
		cmd := exec.Command("sudo", append([]string{"-u", e.user, "udisksctl"}, append(args, "--no-user-interaction", "-b", stick.Device)...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("udisksctl %v: %v: %s", args, err, out)
		}
	}

	// The events socket subscribes the worker to udisks; give it a moment.
	time.Sleep(500 * time.Millisecond)
	udisksctl("unmount")
	waitDrives()
	if v := find(); v.MountPoint != "" || v.CanEject {
		t.Fatalf("after unmount: %+v", v)
	}
	udisksctl("mount")
	waitDrives()
	if v := find(); v.MountPoint == "" {
		t.Fatalf("after mount: %+v", v)
	}

	var res struct{ Unmounted, Ejected bool }
	decode(t, e.do(t, http.MethodPost, "/api/v1/drives/eject", map[string]string{"id": stick.ID}), http.StatusOK, &res)
	if !res.Unmounted {
		t.Fatalf("eject = %+v", res)
	}
	if v := find(); v.MountPoint != "" {
		t.Fatalf("after eject: %+v", v)
	}
	// Leave it mounted for the UI tests.
	udisksctl("mount")
}
