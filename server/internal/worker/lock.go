package worker

import (
	"context"
	"encoding/json"
	"log/slog"
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
	// Source is where the delay came from: "tilde" (the user's own in
	// System Settings, D72), "cinnamon", "gnome", or "default" (neither
	// desktop's schemas are installed).
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

// sessionEvent is one line of the /internal/session stream: "settings"
// carries the lock settings, "lock" is logind asking to lock (D41).
type sessionEvent struct {
	Type string `json:"type"`
	LockSettings
	// Audit is an audit event (D46), for type "audit".
	Audit *protocol.AuditEvent `json:"audit,omitempty"`
}

// internalOnly answers 404 to requests the server did not make for itself
// (protocol.InternalHeader); the proxy never forwards such a request.
func internalOnly(rw http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get(protocol.InternalHeader) == "" {
		writeJSON(rw, http.StatusNotFound, apiError{Error: "no such endpoint", Code: "not_found"})
		return false
	}
	return true
}

// handleSession streams to the server as NDJSON: the lock settings on
// connect and whenever they change, and logind's lock requests. Browsers
// learn the delay from the server.
func (w *Worker) handleSession(rw http.ResponseWriter, r *http.Request) {
	if !internalOnly(rw, r) {
		return
	}
	rw.Header().Set("Content-Type", "application/x-ndjson")
	flusher, _ := rw.(http.Flusher)
	enc := json.NewEncoder(rw)
	send := func(ev sessionEvent) bool {
		if err := enc.Encode(ev); err != nil {
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}
	// logind's lock requests, once the worker has connected to it.
	ready := w.logind.ready
	var locks <-chan struct{}
	t := time.NewTicker(lockSettingsInterval)
	defer t.Stop()
	last := w.lockSettings(r.Context())
	if !send(sessionEvent{Type: "settings", LockSettings: last}) {
		return
	}
	for {
		recheck := false
		select {
		case <-r.Context().Done():
			return
		case <-ready:
			ready = nil
			if w.logind.s != nil {
				locks = w.logind.s.locks()
			}
		case <-locks:
			if !send(sessionEvent{Type: "lock"}) {
				return
			}
		case ev := <-w.auditQ.ch:
			ev = w.auditQ.next(ev)
			if !send(sessionEvent{Type: "audit", Audit: &ev}) {
				return
			}
		// The desktop's settings are read again every minute; Tilde's own
		// reach the server as soon as they are saved.
		case <-t.C:
			recheck = true
		case <-w.settingsChanged:
			recheck = true
		}
		if !recheck {
			continue
		}
		if s := w.lockSettings(r.Context()); s != last {
			if !send(sessionEvent{Type: "settings", LockSettings: s}) {
				return
			}
			last = s
		}
	}
}

// handleSessionLocked sets the logind session's LockedHint: the server
// says whether every browser of the session is locked.
func (w *Worker) handleSessionLocked(rw http.ResponseWriter, r *http.Request) {
	if !internalOnly(rw, r) {
		return
	}
	var req struct {
		Locked bool `json:"locked"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(rw, err)
		return
	}
	session, err := w.logind.wait(r.Context())
	if err != nil {
		writeJSON(rw, http.StatusServiceUnavailable, apiError{Error: "not connected to logind yet", Code: "logind"})
		return
	}
	if session != nil {
		if err := session.setLocked(r.Context(), req.Locked); err != nil {
			slog.Warn("worker: set logind's LockedHint", "locked", req.Locked, "err", err)
			writeJSON(rw, http.StatusBadGateway, apiError{Error: err.Error(), Code: "logind"})
			return
		}
	}
	rw.WriteHeader(http.StatusNoContent)
}
