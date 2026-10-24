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
	"crypto/hmac"
	"crypto/sha1"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"slices"
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
	e := connect(t, os.Getenv("TILDE_USER"))
	resp := e.do(t, http.MethodPost, "/api/auth/login", map[string]string{"username": e.user, "password": os.Getenv("TILDE_PASS")})
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("login: %d %s", resp.StatusCode, body)
	}
	resp.Body.Close()
	return e
}

// connect returns a client for user that has not logged in yet.
func connect(t *testing.T, user string) *env {
	t.Helper()
	raw, pass := os.Getenv("TILDE_URL"), os.Getenv("TILDE_PASS")
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

// TestLockAndUnlock locks every client of the user (D40): a locked client's
// terminal socket is cut and its worker requests refused, while the shell
// keeps running. One client unlocks with the password through the helper's
// verify mode (real PAM) and gets a new cookie; the other stays locked, and
// logging out from it leaves the session alone.
func TestLockAndUnlock(t *testing.T) {
	e, other := setup(t), setup(t)
	var term struct {
		ID string `json:"id"`
	}
	decode(t, e.do(t, http.MethodPost, "/api/v1/terminals", map[string]int{"cols": 80, "rows": 24}), http.StatusCreated, &term)
	defer func() { e.do(t, http.MethodDelete, "/api/v1/terminals/"+term.ID, nil).Body.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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

	decode(t, e.do(t, http.MethodPost, "/api/auth/lock", nil), http.StatusNoContent, nil)
	for {
		// Replayed output may come first; then the socket must close.
		if _, _, err := conn.Read(ctx); err != nil {
			if ctx.Err() != nil {
				t.Fatal("the terminal socket stayed open after locking")
			}
			break
		}
	}
	decode(t, e.do(t, http.MethodGet, "/api/v1/hello", nil), http.StatusLocked, nil)
	decode(t, other.do(t, http.MethodGet, "/api/v1/hello", nil), http.StatusLocked, nil)
	var info struct {
		User   string `json:"user"`
		Locked bool   `json:"locked"`
	}
	decode(t, e.do(t, http.MethodGet, "/api/auth/session", nil), http.StatusOK, &info)
	if !info.Locked || info.User != e.user {
		t.Fatalf("session %+v", info)
	}

	old := e.client.Jar.Cookies(e.base)
	pass := os.Getenv("TILDE_PASS")
	decode(t, e.do(t, http.MethodPost, "/api/auth/unlock", map[string]string{"password": "wrong-" + pass}), http.StatusUnauthorized, nil)
	decode(t, e.do(t, http.MethodPost, "/api/auth/unlock", map[string]string{"password": pass}), http.StatusOK, &info)
	if info.Locked {
		t.Fatal("still locked after unlocking")
	}
	decode(t, e.do(t, http.MethodGet, "/api/v1/hello", nil), http.StatusOK, nil)
	decode(t, other.do(t, http.MethodGet, "/api/v1/hello", nil), http.StatusLocked, nil)

	// The cookie from before the lock is no good any more.
	req, _ := http.NewRequest(http.MethodGet, e.base.String()+"/api/v1/hello", nil)
	for _, c := range old {
		req.AddCookie(c)
	}
	resp, err := (&http.Client{Transport: e.client.Transport}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	decode(t, resp, http.StatusUnauthorized, nil)

	// The shell kept running through the lock.
	type terminal struct {
		ID     string `json:"id"`
		Exited bool   `json:"exited"`
	}
	var list struct {
		Terminals []terminal `json:"terminals"`
	}
	decode(t, e.do(t, http.MethodGet, "/api/v1/terminals", nil), http.StatusOK, &list)
	if !slices.Contains(list.Terminals, terminal{ID: term.ID}) {
		t.Fatalf("terminal %s gone after the lock: %+v", term.ID, list.Terminals)
	}

	decode(t, other.do(t, http.MethodPost, "/api/auth/logout", nil), http.StatusNoContent, nil)
	decode(t, e.do(t, http.MethodGet, "/api/v1/hello", nil), http.StatusOK, nil)
}

// TestLogindLock checks the logind side of the lock (D41): the web
// session's LockedHint follows Tilde's lock, and "loginctl lock-session"
// locks the browsers. It reads logind on this machine, so it only runs
// against a server on loopback.
func TestLogindLock(t *testing.T) {
	e := setup(t)
	if ip := net.ParseIP(e.base.Hostname()); e.base.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		t.Skip("needs the server on this machine")
	}
	id := fields(e.shell(t, "echo session=$XDG_SESSION_ID"))["session"]
	if id == "" {
		t.Fatal("no XDG_SESSION_ID in the terminal")
	}
	lockedHint := func(want string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			out, _ := exec.Command("loginctl", "show-session", id, "-p", "LockedHint", "--value").Output()
			got := strings.TrimSpace(string(out))
			if got == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("LockedHint of session %s is %q, want %q", id, got, want)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	pass := os.Getenv("TILDE_PASS")
	unlock := func() {
		t.Helper()
		decode(t, e.do(t, http.MethodPost, "/api/auth/unlock", map[string]string{"password": pass}), http.StatusOK, nil)
	}

	decode(t, e.do(t, http.MethodPost, "/api/auth/lock", nil), http.StatusNoContent, nil)
	lockedHint("yes")
	unlock()
	lockedHint("no")

	// This test's account is not the Tilde user. A Lock it sends straight
	// to the user's worker, past the worker's match rules, must not count.
	worker := fields(e.shell(t, "echo worker=$PPID"))["worker"]
	list, err := exec.Command("busctl", "--system", "list", "--no-legend").Output()
	if err != nil {
		t.Fatal(err)
	}
	var dest string
	for _, line := range strings.Split(string(list), "\n") {
		if f := strings.Fields(line); len(f) > 1 && strings.HasPrefix(f[0], ":") && f[1] == worker {
			dest = f[0]
		}
	}
	reply, err := exec.Command("busctl", "--system", "call", "org.freedesktop.login1", "/org/freedesktop/login1",
		"org.freedesktop.login1.Manager", "GetSession", "s", id).Output()
	path := strings.Trim(strings.TrimPrefix(strings.TrimSpace(string(reply)), "o "), `"`)
	if dest == "" || err != nil {
		t.Fatalf("worker %s on the bus as %q, session path %q: %v", worker, dest, path, err)
	}
	if out, err := exec.Command("dbus-send", "--system", "--type=signal", "--dest="+dest, path,
		"org.freedesktop.login1.Session.Lock").CombinedOutput(); err != nil {
		t.Fatalf("dbus-send: %v %s", err, out)
	}
	time.Sleep(2 * time.Second)
	decode(t, e.do(t, http.MethodGet, "/api/v1/hello", nil), http.StatusOK, nil)

	if out, err := exec.Command("sudo", "-n", "loginctl", "lock-session", id).CombinedOutput(); err != nil {
		t.Skipf("cannot run loginctl lock-session: %v %s", err, out)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp := e.do(t, http.MethodGet, "/api/v1/hello", nil)
		resp.Body.Close()
		if resp.StatusCode == http.StatusLocked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("still %d after loginctl lock-session", resp.StatusCode)
		}
		time.Sleep(100 * time.Millisecond)
	}
	lockedHint("yes")
	unlock()
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

// TestPolkitMount mounts the test stick through Tilde's polkit agent (D44):
// the VM's rule makes polkit ask the user's own password in a web
// session, the request reaches the browser's API, a wrong password leaves
// it open, the right one mounts, and cancelling refuses the mount.
func TestPolkitMount(t *testing.T) {
	label := os.Getenv("TILDE_USB")
	if label == "" {
		t.Skip("set TILDE_USB to the label of a volume the test user mounted through udisks")
	}
	e := setup(t)
	type request struct {
		ID, Action, Message string
		Identities          []string
	}
	var state struct {
		Agent    bool
		Requests []request
	}
	decode(t, e.do(t, http.MethodGet, "/api/v1/polkit", nil), http.StatusOK, &state)
	if !state.Agent {
		t.Fatal("the worker did not register a polkit agent for its session")
	}
	var res struct {
		Volumes []struct{ ID, Name, MountPoint string }
	}
	decode(t, e.do(t, http.MethodGet, "/api/v1/drives", nil), http.StatusOK, &res)
	id := ""
	for _, v := range res.Volumes {
		if v.Name == label {
			id = v.ID
			if v.MountPoint != "" {
				decode(t, e.do(t, http.MethodPost, "/api/v1/drives/unmount", map[string]string{"id": id}), http.StatusNoContent, nil)
			}
		}
	}
	if id == "" {
		t.Fatalf("%s not listed", label)
	}

	// mount starts a mount, which waits for the dialog, and returns the
	// request polkit made.
	mount := func() (chan *http.Response, request) {
		t.Helper()
		done := make(chan *http.Response, 1)
		go func() {
			b, _ := json.Marshal(map[string]string{"id": id})
			req, _ := http.NewRequest(http.MethodPost, e.base.String()+"/api/v1/drives/mount", bytes.NewReader(b))
			req.Header.Set("Origin", e.base.Scheme+"://"+e.base.Host)
			resp, err := e.client.Do(req)
			if err != nil {
				t.Error(err)
			}
			done <- resp
		}()
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
			decode(t, e.do(t, http.MethodGet, "/api/v1/polkit", nil), http.StatusOK, &state)
			if len(state.Requests) == 1 {
				return done, state.Requests[0]
			}
		}
		t.Fatal("polkit did not ask the agent")
		return nil, request{}
	}

	done, r := mount()
	if !strings.HasPrefix(r.Action, "org.freedesktop.udisks2.filesystem-mount") || !slices.Equal(r.Identities, []string{e.user}) || r.Message == "" {
		t.Fatalf("request %+v", r)
	}
	respond := func(answer string) *http.Response {
		return e.do(t, http.MethodPost, "/api/v1/polkit/respond", map[string]string{"id": r.ID, "answer": answer})
	}
	decode(t, respond("not-the-password"), http.StatusUnauthorized, nil)
	decode(t, respond(os.Getenv("TILDE_PASS")), http.StatusNoContent, nil)
	var vol struct{ MountPoint string }
	decode(t, <-done, http.StatusOK, &vol)
	if vol.MountPoint != "/media/"+e.user+"/"+label {
		t.Fatalf("mounted at %q", vol.MountPoint)
	}
	decode(t, e.do(t, http.MethodPost, "/api/v1/drives/unmount", map[string]string{"id": id}), http.StatusNoContent, nil)

	done, r = mount()
	decode(t, e.do(t, http.MethodPost, "/api/v1/polkit/cancel", map[string]string{"id": r.ID}), http.StatusNoContent, nil)
	var refused struct{ Code string }
	decode(t, <-done, http.StatusForbidden, &refused)
	if refused.Code != "cancelled" {
		t.Fatalf("after cancel: %+v", refused)
	}

	// Leave it mounted for the UI tests.
	if out, err := exec.Command("sudo", "-u", e.user, "udisksctl", "mount", "--no-user-interaction", "-b",
		volumeDevice(t, e, id)).CombinedOutput(); err != nil {
		t.Fatalf("udisksctl mount: %v: %s", err, out)
	}
}

func volumeDevice(t *testing.T, e *env, id string) string {
	t.Helper()
	var res struct {
		Volumes []struct{ ID, Device string }
	}
	decode(t, e.do(t, http.MethodGet, "/api/v1/drives", nil), http.StatusOK, &res)
	for _, v := range res.Volumes {
		if v.ID == id {
			return v.Device
		}
	}
	t.Fatalf("volume %s gone", id)
	return ""
}

// TestAuditLog checks the audit log in the real journal (D46): a failed and
// a successful login, a terminal, and a file moved to the trash, each
// under the test user with the browser's address, tagged tilde-audit.
func TestAuditLog(t *testing.T) {
	since := time.Now().Add(-time.Second).Format("2006-01-02 15:04:05")
	bad := connect(t, os.Getenv("TILDE_USER"))
	decode(t, bad.do(t, http.MethodPost, "/api/auth/login", map[string]string{"username": bad.user, "password": "wrong"}), http.StatusUnauthorized, nil)
	e := setup(t)
	var term struct{ ID string }
	decode(t, e.do(t, http.MethodPost, "/api/v1/terminals", map[string]int{"cols": 80, "rows": 24}), http.StatusCreated, &term)
	decode(t, e.do(t, http.MethodDelete, "/api/v1/terminals/"+term.ID, nil), http.StatusNoContent, nil)
	var hello struct{ Home string }
	decode(t, e.do(t, http.MethodGet, "/api/v1/hello", nil), http.StatusOK, &hello)
	file := hello.Home + "/audit-e2e.txt"
	req, _ := http.NewRequest(http.MethodPut, e.base.String()+"/api/v1/fs/content?overwrite=1&path="+url.QueryEscape(file), strings.NewReader("x"))
	req.Header.Set("Origin", e.base.Scheme+"://"+e.base.Host)
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	decode(t, resp, http.StatusCreated, nil)
	decode(t, e.do(t, http.MethodPost, "/api/v1/fs/trash", map[string][]string{"paths": {file}}), http.StatusOK, nil)

	want := map[string]bool{"login_failed": false, "login": false, "terminal_open": false, "terminal_close": false, "trash": false}
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		out, err := exec.Command("sudo", "journalctl", "--no-pager", "-o", "json", "-t", "tilde-audit",
			"TILDE_USER="+e.user, "--since", since).Output()
		if err != nil {
			t.Fatalf("journalctl: %v", err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			var entry map[string]any
			if json.Unmarshal([]byte(line), &entry) != nil {
				continue
			}
			ev, _ := entry["TILDE_EVENT"].(string)
			if _, ok := want[ev]; !ok {
				continue
			}
			if entry["SYSLOG_FACILITY"] != "10" || entry["_UID"] != "0" {
				t.Fatalf("%s: not root's authpriv record: %v", ev, entry)
			}
			if ev != "terminal_close" && entry["TILDE_RHOST"] != "127.0.0.1" {
				t.Fatalf("%s: rhost %v", ev, entry["TILDE_RHOST"])
			}
			if ev == "trash" && entry["TILDE_PATH"] != file {
				t.Fatalf("trash record: %v", entry)
			}
			want[ev] = true
		}
		if !slices.Contains(slices.Collect(maps.Values(want)), false) {
			return
		}
	}
	t.Fatalf("missing from the journal: %v", want)
}

// totp is RFC 6238's code for counter, with SHA-1 and 6 digits, as pam_oath
// makes it for "HOTP/T30".
func totp(t *testing.T, secretHex string, counter uint64) string {
	t.Helper()
	key, err := hex.DecodeString(secretHex)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha1.New, key)
	binary.Write(mac, binary.BigEndian, counter)
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	n := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", n%1000000)
}

// TestTwoFactor logs in an account that pam_oath asks a TOTP code of (D43):
// the code question reaches the browser after the password, a wrong code
// fails, the right one logs in, and unlocking asks for a code too.
func TestTwoFactor(t *testing.T) {
	user, secret := os.Getenv("TILDE_2FA_USER"), os.Getenv("TILDE_2FA_SECRET")
	if user == "" || secret == "" {
		t.Skip("set TILDE_2FA_USER and TILDE_2FA_SECRET")
	}
	// pam_oath refuses a code twice; the VM script resets its record.
	counter := uint64(time.Now().Unix() / 30)
	code := func() string {
		c := totp(t, secret, counter)
		counter++
		return c
	}
	e := connect(t, user)
	pass := os.Getenv("TILDE_PASS")
	var step struct {
		User   string `json:"user"`
		Prompt *struct {
			Text string `json:"text"`
			Echo bool   `json:"echo"`
		} `json:"prompt"`
	}
	asks := func(resp *http.Response) {
		t.Helper()
		step.Prompt = nil
		decode(t, resp, http.StatusOK, &step)
		if step.Prompt == nil || step.Prompt.Text == "" || step.Prompt.Echo {
			t.Fatalf("no hidden code question: %+v", step)
		}
	}

	asks(e.do(t, http.MethodPost, "/api/auth/login", map[string]string{"username": user, "password": pass}))
	decode(t, e.do(t, http.MethodGet, "/api/v1/hello", nil), http.StatusUnauthorized, nil)
	decode(t, e.do(t, http.MethodPost, "/api/auth/answer", map[string]string{"answer": "000000"}), http.StatusUnauthorized, nil)

	asks(e.do(t, http.MethodPost, "/api/auth/login", map[string]string{"username": user, "password": pass}))
	step.Prompt = nil
	decode(t, e.do(t, http.MethodPost, "/api/auth/answer", map[string]string{"answer": code()}), http.StatusOK, &step)
	if step.Prompt != nil || step.User != user {
		t.Fatalf("after the code: %+v", step)
	}
	defer func() { e.do(t, http.MethodPost, "/api/auth/logout", nil).Body.Close() }()
	decode(t, e.do(t, http.MethodGet, "/api/v1/hello", nil), http.StatusOK, nil)

	decode(t, e.do(t, http.MethodPost, "/api/auth/lock", nil), http.StatusNoContent, nil)
	asks(e.do(t, http.MethodPost, "/api/auth/unlock", map[string]string{"password": pass}))
	decode(t, e.do(t, http.MethodGet, "/api/v1/hello", nil), http.StatusLocked, nil)
	decode(t, e.do(t, http.MethodPost, "/api/auth/answer", map[string]string{"answer": code()}), http.StatusOK, nil)
	decode(t, e.do(t, http.MethodGet, "/api/v1/hello", nil), http.StatusOK, nil)
}
