package worker

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"tilde/internal/protocol"
)

func putSettings(t *testing.T, c *http.Client, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPut, api("/api/v1/settings", nil), bytes.NewReader([]byte(body)))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestSettingsAndTheLockDelay(t *testing.T) {
	c, home := startWorker(t)
	oldCmd, oldEvery := gsettingsCommand, lockSettingsInterval
	t.Cleanup(func() { gsettingsCommand, lockSettingsInterval = oldCmd, oldEvery })
	// No desktop settings; and the stream re-reads them only on a save.
	gsettingsCommand = filepath.Join(t.TempDir(), "missing")
	lockSettingsInterval = time.Hour

	type got struct {
		Settings     Settings
		DesktopLock  LockSettings
		DesktopTheme DesktopTheme
	}
	g := decode[got](t, mustGet(t, c, api("/api/v1/settings", nil)), http.StatusOK)
	if g.Settings != defaultSettings() || g.DesktopLock != defaultLockSettings() || g.DesktopTheme != defaultDesktopTheme() {
		t.Fatalf("defaults: %+v", g)
	}

	req, _ := http.NewRequest(http.MethodGet, api("/internal/session", nil), nil)
	req.Header.Set(protocol.InternalHeader, "1")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	lines := bufio.NewScanner(resp.Body)
	next := func() LockSettings {
		t.Helper()
		done := make(chan LockSettings, 1)
		go func() {
			for lines.Scan() {
				var ev sessionEvent
				if json.Unmarshal(lines.Bytes(), &ev) == nil && ev.Type == "settings" {
					done <- ev.LockSettings
					return
				}
			}
		}()
		select {
		case s := <-done:
			return s
		case <-time.After(5 * time.Second):
			t.Fatal("no lock settings")
		}
		return LockSettings{}
	}
	if s := next(); s != defaultLockSettings() {
		t.Fatalf("first: %+v", s)
	}

	// Tilde's own delay reaches the server at once, not at the next minute.
	s := defaultSettings()
	s.Lock = LockPrefs{Own: true, Enabled: true, Minutes: 5}
	s.Effects.Animations = false
	body, _ := json.Marshal(s)
	decode[Settings](t, putSettings(t, c, string(body)), http.StatusOK)
	if got := next(); got != (LockSettings{Source: "tilde", LockAfter: 300}) {
		t.Fatalf("own delay: %+v", got)
	}
	file := filepath.Join(home, ".config", "tilde", "settings.json")
	if st, err := os.Stat(file); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("settings file: %v %v", st, err)
	}
	if g := decode[got](t, mustGet(t, c, api("/api/v1/settings", nil)), http.StatusOK); g.Settings != s {
		t.Fatalf("read back: %+v", g.Settings)
	}

	s.Lock.Enabled = false
	body, _ = json.Marshal(s)
	decode[Settings](t, putSettings(t, c, string(body)), http.StatusOK)
	if got := next(); got != (LockSettings{Source: "tilde"}) {
		t.Fatalf("never: %+v", got)
	}
	// Back to the desktop's.
	s.Lock.Own = false
	body, _ = json.Marshal(s)
	decode[Settings](t, putSettings(t, c, string(body)), http.StatusOK)
	if got := next(); got != defaultLockSettings() {
		t.Fatalf("desktop again: %+v", got)
	}

	for _, bad := range []string{`{"lock":{"own":true,"enabled":true,"minutes":0}}`, `{"lock":{"minutes":99999}}`, `not json`,
		`{"lock":{"minutes":5},"appearance":{"mode":"neon","accent":"green"}}`, `{"lock":{"minutes":5},"appearance":{"mode":"dark","accent":"beige"}}`} {
		decode[apiError](t, putSettings(t, c, bad), http.StatusBadRequest)
	}
}

func TestSettingsFileFromOlderOrBroken(t *testing.T) {
	w := driveWorker(t, nil)
	file := w.settingsFile()
	os.MkdirAll(filepath.Dir(file), 0o700)

	// An older file without the panel section: those keep their defaults.
	os.WriteFile(file, []byte(`{"effects":{"animations":false}}`), 0o600)
	want := defaultSettings()
	want.Effects.Animations = false
	if got := w.loadSettings(); got != want {
		t.Fatalf("older file: %+v", got)
	}
	os.WriteFile(file, []byte(`{broken`), 0o600)
	if got := w.loadSettings(); got != defaultSettings() {
		t.Fatalf("broken file: %+v", got)
	}
	// Not a regular file: the defaults, at once.
	os.Remove(file)
	if err := syscall.Mkfifo(file, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := w.loadSettings(); got != defaultSettings() {
		t.Fatalf("fifo: %+v", got)
	}
}
