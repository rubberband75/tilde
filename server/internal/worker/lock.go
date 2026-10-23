package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"tilde/internal/protocol"
)

// Idle lock settings (D40). A client that has had no input for the user's
// screensaver delay locks, as the screen does on Mint (D15). The server
// keeps the lock; the worker only reads the delay, because it lives in the
// user's dconf database, which the server must not open (it never touches
// user files).

// LockSettings says when an idle client locks.
type LockSettings struct {
	// Source is where the delay came from: "cinnamon", "gnome", or
	// "default" (neither desktop's schemas are installed).
	Source string `json:"source"`
	// LockAfter is how many seconds without input lock a client; 0 means
	// never (the screensaver or its lock is turned off).
	LockAfter int `json:"lockAfter"`
}

// lockSchemas are the desktops whose settings Tilde follows, in order.
var lockSchemas = []struct{ source, session, screensaver string }{
	{"cinnamon", "org.cinnamon.desktop.session", "org.cinnamon.desktop.screensaver"},
	{"gnome", "org.gnome.desktop.session", "org.gnome.desktop.screensaver"},
}

// defaultLockSettings are Cinnamon's defaults: lock after 15 minutes.
func defaultLockSettings() LockSettings { return LockSettings{Source: "default", LockAfter: 900} }

// readLockSettings follows the first desktop whose schemas are installed.
func readLockSettings(ctx context.Context) LockSettings {
	for _, s := range lockSchemas {
		session, ok := gsettingsList(ctx, s.session)
		if !ok {
			continue
		}
		saver, ok := gsettingsList(ctx, s.screensaver)
		if !ok {
			continue
		}
		return lockSettingsFrom(s.source, session, saver)
	}
	return defaultLockSettings()
}

// lockSettingsFrom applies the screensaver's rules: after idle-delay
// seconds the screensaver starts (if idle-activation-enabled), and
// lock-delay seconds later it locks (if lock-enabled). Tilde has no
// screensaver to show in between, so it locks at the sum.
func lockSettingsFrom(source string, session, saver map[string]string) LockSettings {
	idle := gvariantUint(session["idle-delay"])
	if idle == 0 || !gvariantBool(saver["idle-activation-enabled"], true) || !gvariantBool(saver["lock-enabled"], true) {
		return LockSettings{Source: source}
	}
	return LockSettings{Source: source, LockAfter: idle + gvariantUint(saver["lock-delay"])}
}

// gvariantUint parses GVariant text such as "uint32 900"; anything else is 0.
func gvariantUint(s string) int {
	s = strings.TrimSpace(s)
	if _, v, ok := strings.Cut(s, " "); ok {
		s = v // drop a type annotation
	}
	n, err := strconv.ParseUint(s, 10, 31)
	if err != nil {
		return 0
	}
	return int(n)
}

// gvariantBool parses "true" or "false", or returns def.
func gvariantBool(s string, def bool) bool {
	switch strings.TrimSpace(s) {
	case "true":
		return true
	case "false":
		return false
	}
	return def
}

// lockSettingsInterval is how often the settings are read again; tests
// shorten it.
var lockSettingsInterval = time.Minute

// handleLockSettings streams the lock settings to the server as NDJSON,
// once on connect and then whenever they change. Only the server asks
// (protocol.InternalHeader); browsers learn the delay from the server.
func (w *Worker) handleLockSettings(rw http.ResponseWriter, r *http.Request) {
	if r.Header.Get(protocol.InternalHeader) == "" {
		writeJSON(rw, http.StatusNotFound, apiError{Error: "no such endpoint", Code: "not_found"})
		return
	}
	rw.Header().Set("Content-Type", "application/x-ndjson")
	flusher, _ := rw.(http.Flusher)
	enc := json.NewEncoder(rw)
	t := time.NewTicker(lockSettingsInterval)
	defer t.Stop()
	var last LockSettings
	for first := true; ; first = false {
		if s := readLockSettings(r.Context()); first || s != last {
			if err := enc.Encode(s); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
			last = s
		}
		select {
		case <-r.Context().Done():
			return
		case <-t.C:
		}
	}
}
