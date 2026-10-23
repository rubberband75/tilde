package worker

import (
	"bufio"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

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
	resp, err := c.Get(api("/internal/lock-settings", nil))
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

	req, _ := http.NewRequest(http.MethodGet, api("/internal/lock-settings", nil), nil)
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
		var s LockSettings
		if err := json.Unmarshal(lines.Bytes(), &s); err != nil {
			t.Fatal(err)
		}
		return s
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
