package worker

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"tilde/internal/protocol"
)

// fakeAgentHelper writes a stand-in for polkit-agent-helper-1 that speaks
// its protocol: the cookie on stdin, PAM's messages on stdout, then the
// verdict. "secret" is every user's password; user "coded" is also asked
// for a code. Each success is appended to the returned file.
func fakeAgentHelper(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "authorized")
	script := `#!/bin/sh
read cookie
echo "PAM_PROMPT_ECHO_OFF Password: "
read pw
if [ "$pw" != secret ]; then echo "PAM_ERROR_MSG Wrong password"; echo FAILURE; exit 0; fi
if [ "$1" = coded ]; then
  printf '%s\n' 'PAM_TEXT_INFO Code sent\tby SMS'
  echo "PAM_PROMPT_ECHO_OFF Verification code: "
  read code
  [ "$code" = 123456 ] || { echo FAILURE; exit 0; }
fi
echo "$1 $cookie" >> ` + out + `
echo SUCCESS
exec sleep 10
`
	path := filepath.Join(dir, "polkit-agent-helper-1")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, out
}

func testAgent(t *testing.T) (*polkitAgent, *atomic.Int32, string) {
	t.Helper()
	var events atomic.Int32
	a := newPolkitAgent("me", func() { events.Add(1) })
	var out string
	a.helper, out = fakeAgentHelper(t)
	t.Cleanup(a.close)
	return a, &events, out
}

func wait(t *testing.T, r *authRequest) *dbus.Error {
	t.Helper()
	select {
	case <-r.done:
		return r.result
	case <-time.After(5 * time.Second):
		t.Fatal("the request did not end")
		return nil
	}
}

func authorized(t *testing.T, out string) string {
	t.Helper()
	b, _ := os.ReadFile(out)
	return string(b)
}

// TestAgentPassword: the password goes to the helper's first hidden
// prompt; a wrong one leaves the request open for another try, and the
// right one ends it, which answers polkit (D44).
func TestAgentPassword(t *testing.T) {
	a, events, out := testAgent(t)
	var audited []protocol.AuditEvent
	a.audit = func(ev protocol.AuditEvent) { audited = append(audited, ev) }
	r, derr := a.begin("org.freedesktop.udisks2.filesystem-mount", "Authentication is required to mount STICK", "drive-removable-media", "cookie-1", []string{"me", "admin"})
	if derr != nil {
		t.Fatal(derr)
	}
	if l := a.list(); len(l) != 1 || l[0].ID != r.ID || l[0].Message != "Authentication is required to mount STICK" || events.Load() != 1 {
		t.Fatalf("list %+v, %d events", l, events.Load())
	}

	if _, err := a.respond(r.ID, "mallory", "secret", ""); !errors.As(err, new(badRequest)) {
		t.Fatalf("someone polkit did not list: %v", err)
	}
	_, err := a.respond(r.ID, "", "wrong", "")
	var failed authFailed
	if !errors.As(err, &failed) || failed.Error() != "Wrong password" {
		t.Fatalf("wrong password: %v", err)
	}
	if len(a.list()) != 1 {
		t.Fatal("a wrong password ended the request")
	}
	if p, err := a.respond(r.ID, "", "secret", ""); p != nil || err != nil {
		t.Fatalf("right password: %v %v", p, err)
	}
	if res := wait(t, r); res != nil {
		t.Fatalf("result %v", res)
	}
	if got := authorized(t, out); got != "me cookie-1\n" {
		t.Fatalf("helper authorized %q", got)
	}
	mount := "org.freedesktop.udisks2.filesystem-mount"
	if want := []protocol.AuditEvent{
		{Event: "polkit", Action: mount, Identity: "me", Result: "failed"},
		{Event: "polkit", Action: mount, Identity: "me", Result: "granted"},
	}; !reflect.DeepEqual(audited, want) {
		t.Fatalf("audited %+v", audited)
	}
	if len(a.list()) != 0 {
		t.Fatal("the request is still listed")
	}
	if _, err := a.respond(r.ID, "", "secret", ""); !errors.Is(err, errNoAuthRequest) {
		t.Fatalf("after the end: %v", err)
	}
}

// TestAgentCode: a question after the password goes to the browser with
// PAM's messages, and is listed for the other browsers too.
func TestAgentCode(t *testing.T) {
	a, _, out := testAgent(t)
	r, _ := a.begin("org.test", "Test", "", "cookie-2", []string{"me", "coded"})
	p, err := a.respond(r.ID, "coded", "secret", "")
	if err != nil || p == nil || p.Text != "Verification code:" || p.Echo || !reflect.DeepEqual(p.Info, []string{"Code sent\tby SMS"}) {
		t.Fatalf("prompt %+v, %v", p, err)
	}
	if l := a.list(); l[0].Prompt == nil || l[0].Prompt.Text != "Verification code:" {
		t.Fatalf("list %+v", l)
	}
	if p, err := a.respond(r.ID, "", "123456", ""); p != nil || err != nil {
		t.Fatalf("code: %v %v", p, err)
	}
	if res := wait(t, r); res != nil {
		t.Fatalf("result %v", res)
	}
	if got := authorized(t, out); got != "coded cookie-2\n" {
		t.Fatalf("helper authorized %q", got)
	}
}

// TestAgentCancel: cancelling or not answering ends the request as
// cancelled, which polkit reports to the program that asked.
func TestAgentCancel(t *testing.T) {
	a, _, _ := testAgent(t)
	r, _ := a.begin("org.test", "Test", "", "cookie-3", []string{"coded"})
	if _, err := a.respond(r.ID, "", "secret", ""); err != nil {
		t.Fatal(err)
	}
	if err := a.cancelRequest(r.ID); err != nil {
		t.Fatal(err)
	}
	if res := wait(t, r); res == nil || res.Name != polkitCancelled {
		t.Fatalf("result %v", res)
	}
	if _, err := a.respond(r.ID, "", "123456", ""); !errors.Is(err, errNoAuthRequest) {
		t.Fatalf("answer after cancel: %v", err)
	}

	old := authTimeout
	authTimeout = 50 * time.Millisecond
	t.Cleanup(func() { authTimeout = old })
	r, _ = a.begin("org.test", "Test", "", "cookie-4", []string{"me"})
	if res := wait(t, r); res == nil || res.Name != polkitCancelled {
		t.Fatalf("timeout result %v", res)
	}
}

func TestAgentLimits(t *testing.T) {
	a, _, _ := testAgent(t)
	if _, derr := a.begin("org.test", "Test", "", "c", nil); derr == nil {
		t.Fatal("a request nobody can answer was held")
	}
	for i := range maxAuthRequests {
		if _, derr := a.begin("org.test", "Test", "", strconv.Itoa(i), []string{"me"}); derr != nil {
			t.Fatal(derr)
		}
	}
	if _, derr := a.begin("org.test", "Test", "", "one more", []string{"me"}); derr == nil {
		t.Fatal("no limit on waiting requests")
	}
}

// TestAgentOnlyPolkit: calls from anyone but polkit's bus name are refused
// before a dialog reaches the browser.
func TestAgentOnlyPolkit(t *testing.T) {
	a, events, _ := testAgent(t)
	o := agentObject{a}
	if err := o.BeginAuthentication(":1.666", "org.test", "Type your password", "", nil, "c", []polkitIdentity{{Kind: "unix-user"}}); err == nil {
		t.Fatal("a call from another bus name was taken")
	}
	if events.Load() != 0 || len(a.list()) != 0 {
		t.Fatal("the browser was asked")
	}
}

func TestIdentityNames(t *testing.T) {
	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	groupFile = filepath.Join(t.TempDir(), "group")
	t.Cleanup(func() { groupFile = "/etc/group" })
	os.WriteFile(groupFile, []byte("root:x:0:\nsudo:x:27:alice,"+me.Username+",bob\nempty:x:30:\n"), 0o644)
	uid, _ := strconv.ParseUint(me.Uid, 10, 32)
	ids := []polkitIdentity{
		{Kind: "unix-group", Details: map[string]dbus.Variant{"gid": dbus.MakeVariant(uint32(27))}},
		{Kind: "unix-user", Details: map[string]dbus.Variant{"uid": dbus.MakeVariant(uint32(uid))}},
		{Kind: "unix-group", Details: map[string]dbus.Variant{"gid": dbus.MakeVariant(uint32(30))}},
		{Kind: "unix-netgroup", Details: map[string]dbus.Variant{"name": dbus.MakeVariant("x")}},
	}
	if got, want := identityNames(ids, me.Username), []string{me.Username, "alice", "bob"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("names %v, want %v", got, want)
	}
}

func TestStrcompress(t *testing.T) {
	for in, want := range map[string]string{
		`Password: `:         "Password: ",
		`a\tb\\c\"d\n`:       "a\tb\\c\"d\n",
		`caf\303\251 \101\1`: "café A\x01",
		`trailing\`:          `trailing\`,
	} {
		if got := strcompress(in); got != want {
			t.Errorf("strcompress(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestPolkitAPI: the browser's side, through the worker's routes.
func TestPolkitAPI(t *testing.T) {
	w := driveWorker(t, nil)
	w.polkit.helper, _ = fakeAgentHelper(t)
	r, _ := w.polkit.begin("org.test", "Test", "", "cookie-5", []string{"me", "coded"})
	rec := serve(t, w, "GET", "/api/v1/polkit", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"identities":["me","coded"]`) || !strings.Contains(rec.Body.String(), `"agent":false`) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	for _, c := range []struct {
		body string
		code int
		want string
	}{
		{`{"id":"` + r.ID + `","answer":"wrong"}`, 401, `"code":"auth_failed"`},
		{`{"id":"nope","answer":"secret"}`, 404, `"code":"not_found"`},
		{`{"id":"` + r.ID + `","identity":"coded","answer":"secret"}`, 200, `"text":"Verification code:"`},
		{`{"id":"` + r.ID + `","answer":"1\n2"}`, 400, `"code":"bad_request"`},
		{`{"id":"` + r.ID + `","answer":"` + strings.Repeat("x", 512) + `"}`, 400, `too long`},
	} {
		rec := serve(t, w, "POST", "/api/v1/polkit/respond", c.body)
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("respond %s: %d %s", c.body, rec.Code, rec.Body)
		}
	}
	if rec := serve(t, w, "POST", "/api/v1/polkit/cancel", `{"id":"`+r.ID+`"}`); rec.Code != 204 {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body)
	}
	if rec := serve(t, w, "POST", "/api/v1/polkit/cancel", `{"id":"`+r.ID+`"}`); rec.Code != 404 {
		t.Fatalf("cancel again: %d %s", rec.Code, rec.Body)
	}
}
