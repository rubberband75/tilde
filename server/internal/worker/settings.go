package worker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
)

// Tilde's own settings (D67, D72), in ~/.config/tilde/settings.json,
// written by the worker as the user. A desktop's settings (Cinnamon's,
// GNOME's) are read as defaults and never written: the lock delay follows
// the desktop's unless the user sets Tilde's own.

// Settings are the System Settings app's.
type Settings struct {
	Lock    LockPrefs  `json:"lock"`
	Effects Effects    `json:"effects"`
	Panel   PanelPrefs `json:"panel"`
}

// LockPrefs say when an idle browser locks (D40).
type LockPrefs struct {
	// Own: Tilde's own delay below, rather than the desktop's.
	Own bool `json:"own"`
	// Enabled and Minutes are Tilde's own: lock after this many minutes
	// without input, or never.
	Enabled bool `json:"enabled"`
	Minutes int  `json:"minutes"`
}

// Effects: window animations (D57).
type Effects struct {
	Animations bool `json:"animations"`
}

// PanelPrefs: which applets the panel shows.
type PanelPrefs struct {
	Resources     bool `json:"resources"`
	Network       bool `json:"network"`
	Notifications bool `json:"notifications"`
	Fullscreen    bool `json:"fullscreen"`
}

// defaultSettings: the desktop's lock delay, animations, every applet.
func defaultSettings() Settings {
	return Settings{
		Lock:    LockPrefs{Enabled: true, Minutes: 15},
		Effects: Effects{Animations: true},
		Panel:   PanelPrefs{Resources: true, Network: true, Notifications: true, Fullscreen: true},
	}
}

// maxLockMinutes matches the server's clamp: a day.
const maxLockMinutes = 24 * 60

func (s Settings) valid() error {
	if s.Lock.Minutes < 1 || s.Lock.Minutes > maxLockMinutes {
		return badRequest{"the lock delay must be between 1 minute and a day"}
	}
	return nil
}

func (w *Worker) settingsFile() string {
	return filepath.Join(w.configHome, "tilde", "settings.json")
}

// maxSettingsFile is far more than the settings take.
const maxSettingsFile = 64 << 10

// loadSettings reads the settings file; anything missing from it (a file
// from an older Tilde) keeps its default. It runs on the session stream's
// every tick, so a FIFO or /dev/zero put in its place must not hang or
// fill the worker: only a regular file is read, and only so much of it.
func (w *Worker) loadSettings() Settings {
	s := defaultSettings()
	f, err := os.OpenFile(w.settingsFile(), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return s
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return s
	}
	data, err := io.ReadAll(io.LimitReader(f, maxSettingsFile))
	if err != nil {
		return s
	}
	if json.Unmarshal(data, &s) != nil || s.valid() != nil {
		return defaultSettings()
	}
	return s
}

// lockSettings are the ones the server is told: Tilde's own if the user
// set them, else the desktop's.
func (w *Worker) lockSettings(ctx context.Context) LockSettings {
	if p := w.loadSettings().Lock; p.Own {
		if !p.Enabled {
			return LockSettings{Source: "tilde"}
		}
		return LockSettings{Source: "tilde", LockAfter: p.Minutes * 60}
	}
	return readLockSettings(ctx)
}

// handleSettings returns the settings, and what the desktop says about
// locking, for "follow the desktop".
func (w *Worker) handleSettings(rw http.ResponseWriter, r *http.Request) {
	writeJSON(rw, http.StatusOK, map[string]any{
		"settings":    w.loadSettings(),
		"desktopLock": readLockSettings(r.Context()),
	})
}

// handleSettingsSave replaces the settings. Every browser hears of it
// (a "settings" event), and the server of the lock delay at once.
func (w *Worker) handleSettingsSave(rw http.ResponseWriter, r *http.Request) {
	var s Settings
	if err := decodeJSON(r, &s); err != nil {
		writeErr(rw, err)
		return
	}
	if err := s.valid(); err != nil {
		writeErr(rw, err)
		return
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		writeErr(rw, err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(w.settingsFile()), 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		writeErr(rw, err)
		return
	}
	if err := writeFileAtomic(w.settingsFile(), append(data, '\n'), 0o600); err != nil {
		writeErr(rw, err)
		return
	}
	w.events.publish(event{Type: "settings"})
	select {
	case w.settingsChanged <- struct{}{}:
	default: // a change already waiting covers this one
	}
	writeJSON(rw, http.StatusOK, s)
}
