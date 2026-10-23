package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"tilde/internal/protocol"
)

func TestLockSettingsFrom(t *testing.T) {
	on := map[string]string{"idle-activation-enabled": "true", "lock-enabled": "true", "lock-delay": "uint32 0"}
	tests := []struct {
		name           string
		session, saver map[string]string
		want           int
	}{
		{"Mint default", map[string]string{"idle-delay": "uint32 900"}, on, 900},
		{"lock delay adds", map[string]string{"idle-delay": "uint32 300"},
			map[string]string{"lock-enabled": "true", "lock-delay": "uint32 60"}, 360},
		{"lock off", map[string]string{"idle-delay": "uint32 300"},
			map[string]string{"idle-activation-enabled": "true", "lock-enabled": "false"}, 0},
		{"screensaver off", map[string]string{"idle-delay": "uint32 300"},
			map[string]string{"idle-activation-enabled": "false", "lock-enabled": "true"}, 0},
		{"never idle", map[string]string{"idle-delay": "uint32 0"}, on, 0},
		{"missing keys", map[string]string{}, map[string]string{}, 0},
		{"no idle-activation key (GNOME)", map[string]string{"idle-delay": "uint32 600"},
			map[string]string{"lock-enabled": "true", "lock-delay": "uint32 30"}, 630},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lockSettingsFrom("cinnamon", tt.session, tt.saver)
			if got != (LockSettings{Source: "cinnamon", LockAfter: tt.want}) {
				t.Fatalf("got %+v, want lockAfter %d", got, tt.want)
			}
		})
	}
}

func TestLockSettingsStream(t *testing.T) {
	c, home := startWorker(t)
	oldCmd, oldEvery := gsettingsCommand, lockSettingsInterval
	t.Cleanup(func() { gsettingsCommand, lockSettingsInterval = oldCmd, oldEvery })
	lockSettingsInterval = 20 * time.Millisecond

	// Browsers cannot reach it: the server's proxy never sends the header.
	resp, err := c.Get(api("/internal/session", nil))
	if err != nil {
		t.Fatal(err)
	}
	decode[apiError](t, resp, http.StatusNotFound)

	// Only GNOME's schemas, with an idle delay the test changes.
	delay := filepath.Join(home, "idle-delay")
	write(t, delay, "300")
	gsettingsCommand = fakeCommand(t, "gsettings", `case "$2" in
org.gnome.desktop.session) echo "org.gnome.desktop.session idle-delay uint32 $(cat '`+delay+`')" ;;
org.gnome.desktop.screensaver) printf '%s\n' "org.gnome.desktop.screensaver lock-enabled true" \
  "org.gnome.desktop.screensaver lock-delay uint32 30" ;;
*) echo "No such schema" >&2; exit 1 ;;
esac
`)

	req, _ := http.NewRequest(http.MethodGet, api("/internal/session", nil), nil)
	req.Header.Set(protocol.InternalHeader, "1")
	resp, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	lines := bufio.NewScanner(resp.Body)
	next := func() LockSettings {
		t.Helper()
		if !lines.Scan() {
			t.Fatalf("stream ended: %v", lines.Err())
		}
		var ev sessionEvent
		if err := json.Unmarshal(lines.Bytes(), &ev); err != nil || ev.Type != "settings" {
			t.Fatalf("%s: %v", lines.Bytes(), err)
		}
		return ev.LockSettings
	}
	if s := next(); s != (LockSettings{Source: "gnome", LockAfter: 330}) {
		t.Fatalf("first: %+v", s)
	}
	// Unchanged settings are not sent again; a change is.
	time.Sleep(5 * lockSettingsInterval)
	write(t, delay, "0")
	if s := next(); s != (LockSettings{Source: "gnome", LockAfter: 0}) {
		t.Fatalf("after turning idle off: %+v", s)
	}
}

func TestLockSettingsWithoutGsettings(t *testing.T) {
	old := gsettingsCommand
	t.Cleanup(func() { gsettingsCommand = old })
	gsettingsCommand = filepath.Join(t.TempDir(), "missing")
	if s := readLockSettings(t.Context()); s != defaultLockSettings() {
		t.Fatalf("got %+v", s)
	}
}

// fakeLogind stands in for the worker's logind session.
type fakeLogind struct {
	mu    sync.Mutex
	hints []bool
	lock  chan struct{}
}

func (f *fakeLogind) setLocked(_ context.Context, locked bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hints = append(f.hints, locked)
	return nil
}

func (f *fakeLogind) locks() <-chan struct{} { return f.lock }
func (f *fakeLogind) close()                 {}

// TestLogindSession: the server sets the session's LockedHint through the
// worker, and logind's lock requests reach the server on the stream.
func TestLogindSession(t *testing.T) {
	fake := &fakeLogind{lock: make(chan struct{}, 1)}
	// Connected after the server has opened the stream, as in a real
	// session: the worker connects to logind after its hello.
	logind := &logindConn{id: "1", ready: make(chan struct{})}
	c, _ := startWorkerWith(t, logind)
	internal := func(method, path, body string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(method, api(path, nil), strings.NewReader(body))
		req.Header.Set(protocol.InternalHeader, "1")
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	resp := internal(http.MethodGet, "/internal/session", "")
	defer resp.Body.Close()
	lines := bufio.NewScanner(resp.Body)
	var ev sessionEvent
	if !lines.Scan() || json.Unmarshal(lines.Bytes(), &ev) != nil || ev.Type != "settings" {
		t.Fatalf("first line %q", lines.Bytes())
	}
	logind.s = fake
	close(logind.ready)

	req, _ := http.NewRequest(http.MethodPut, api("/internal/session/locked", nil), strings.NewReader(`{"locked":true}`))
	put, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	decode[apiError](t, put, http.StatusNotFound)
	decode[struct{}](t, internal(http.MethodPut, "/internal/session/locked", `{"locked":true}`), http.StatusNoContent)
	decode[struct{}](t, internal(http.MethodPut, "/internal/session/locked", `{"locked":false}`), http.StatusNoContent)
	fake.mu.Lock()
	if got := fake.hints; len(got) != 2 || !got[0] || got[1] {
		t.Fatalf("hints %v, want [true false]", got)
	}
	fake.mu.Unlock()

	fake.lock <- struct{}{}
	if !lines.Scan() || json.Unmarshal(lines.Bytes(), &ev) != nil || ev.Type != "lock" {
		t.Fatalf("after logind's lock request: %q", lines.Bytes())
	}
}

// TestLockWatcher: any user on the system bus can send a signal straight
// to the worker; only logind's Lock for this session counts.
func TestLockWatcher(t *testing.T) {
	path := dbus.ObjectPath("/org/freedesktop/login1/session/_312")
	w := &lockWatcher{path: path, owner: ":1.3"}
	lock := func(sender string, p dbus.ObjectPath) bool {
		return w.isLock(&dbus.Signal{Sender: sender, Path: p, Name: sessionIface + ".Lock"})
	}
	owner := func(sender, name, newOwner string) {
		w.isLock(&dbus.Signal{Sender: sender, Path: "/org/freedesktop/DBus", Name: busDaemon + ".NameOwnerChanged",
			Body: []any{name, ":1.3", newOwner}})
	}
	if !lock(":1.3", path) {
		t.Fatal("logind's Lock refused")
	}
	if lock(":1.99", path) {
		t.Fatal("a Lock from another peer counted")
	}
	if lock(":1.3", "/org/freedesktop/login1/session/_313") {
		t.Fatal("another session's Lock counted")
	}
	owner(":1.99", logindName, ":1.99")
	if lock(":1.99", path) {
		t.Fatal("a peer took logind's name with a fake NameOwnerChanged")
	}
	owner(busDaemon, "org.example.Other", ":1.50")
	owner(busDaemon, logindName, ":1.40")
	if lock(":1.3", path) || !lock(":1.40", path) {
		t.Fatal("did not follow logind to its new bus name")
	}
}
