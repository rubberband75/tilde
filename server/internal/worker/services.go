package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"tilde/internal/protocol"
)

// The System Monitor's Services tab (D60): systemd's services, the
// system's on the system bus and the user's own from their user manager
// on the session bus, as the user. systemd and polkit decide who may start
// and stop what, as udisks and NetworkManager do for drives and networks;
// the user's own manager needs no polkit.

const (
	systemdName    = "org.freedesktop.systemd1"
	systemdPath    = "/org/freedesktop/systemd1"
	systemdManager = systemdName + ".Manager"
	systemdUnit    = systemdName + ".Unit"
)

// Service is one row of the Services tab.
type Service struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Load is systemd's load state: loaded, not-found, masked..., or empty
	// for a unit file not loaded now.
	Load string `json:"load,omitempty"`
	// Active and Sub are systemd's states: active/running, active/exited,
	// inactive/dead, failed/failed, activating/start...
	Active string `json:"active"`
	Sub    string `json:"sub"`
	// Boot is the unit file's state: enabled, disabled, static, masked,
	// indirect, generated...; empty without a unit file.
	Boot string `json:"boot,omitempty"`
}

// unitStatus is one entry of ListUnitsByPatterns.
type unitStatus struct {
	Name, Description, Load, Active, Sub, Followed string
	Path                                           dbus.ObjectPath
	JobID                                          uint32
	JobType                                        string
	JobPath                                        dbus.ObjectPath
}

// unitFile is one entry of ListUnitFilesByPatterns.
type unitFile struct {
	Path, State string
}

// systemdSource is what Tilde needs from one systemd manager; tests
// replace it.
type systemdSource interface {
	units(ctx context.Context) ([]unitStatus, error)
	unitFiles(ctx context.Context) ([]unitFile, error)
	// call runs a Manager method; interactive lets polkit ask for a password.
	// The first argument names the unit (or units, for unit-file methods).
	call(ctx context.Context, method string, interactive bool, args ...any) error
	state(ctx context.Context, name string) (active, sub string, err error)
}

type busSystemd struct{ conn *dbus.Conn }

func (b *busSystemd) manager() dbus.BusObject { return b.conn.Object(systemdName, systemdPath) }

func (b *busSystemd) units(ctx context.Context) ([]unitStatus, error) {
	var out []unitStatus
	err := b.manager().CallWithContext(ctx, systemdManager+".ListUnitsByPatterns", 0,
		[]string{}, []string{"*.service"}).Store(&out)
	return out, err
}

func (b *busSystemd) unitFiles(ctx context.Context) ([]unitFile, error) {
	var out []unitFile
	err := b.manager().CallWithContext(ctx, systemdManager+".ListUnitFilesByPatterns", 0,
		[]string{}, []string{"*.service"}).Store(&out)
	return out, err
}

func (b *busSystemd) call(ctx context.Context, method string, interactive bool, args ...any) error {
	return callWith(ctx, b.conn, systemdName, systemdPath, systemdManager+"."+method, interactive, args...).Err
}

func (b *busSystemd) state(ctx context.Context, name string) (string, string, error) {
	var path dbus.ObjectPath
	if err := b.manager().CallWithContext(ctx, systemdManager+".GetUnit", 0, name).Store(&path); err != nil {
		var de dbus.Error
		if errors.As(err, &de) && de.Name == systemdName+".NoSuchUnit" {
			return "inactive", "dead", nil // unloaded once stopped
		}
		return "", "", err
	}
	obj := b.conn.Object(systemdName, path)
	active, err := obj.GetProperty(systemdUnit + ".ActiveState")
	if err != nil {
		return "", "", err
	}
	sub, err := obj.GetProperty(systemdUnit + ".SubState")
	if err != nil {
		return "", "", err
	}
	a, _ := active.Value().(string)
	s, _ := sub.Value().(string)
	return a, s, nil
}

// serviceManagers connects to the system's and the user's systemd on first
// use, and again a minute after a failure.
type serviceManagers struct {
	mu       sync.Mutex
	src      map[string]systemdSource
	failedAt map[string]time.Time
	conns    []*dbus.Conn
}

// sessionBusAddress: pam_systemd sets it; the socket is always here too.
func sessionBusAddress() string {
	if a := os.Getenv("DBUS_SESSION_BUS_ADDRESS"); a != "" {
		return a
	}
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return "unix:path=" + filepath.Join(d, "bus")
	}
	return ""
}

func (m *serviceManagers) source(scope string) systemdSource {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.src == nil {
		m.src, m.failedAt = map[string]systemdSource{}, map[string]time.Time{}
	}
	if s := m.src[scope]; s != nil || time.Since(m.failedAt[scope]) < time.Minute {
		return s
	}
	var conn *dbus.Conn
	var err error
	if scope == "system" {
		conn, err = dbus.ConnectSystemBus()
	} else if addr := sessionBusAddress(); addr != "" {
		conn, err = dbus.Connect(addr)
	} else {
		err = errors.New("no session bus")
	}
	if err != nil {
		slog.Info("worker: no systemd", "scope", scope, "err", err)
		m.failedAt[scope] = time.Now()
		return nil
	}
	m.conns = append(m.conns, conn)
	m.src[scope] = &busSystemd{conn: conn}
	return m.src[scope]
}

func (m *serviceManagers) close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.conns {
		c.Close()
	}
}

// servicesFrom merges the loaded units with the unit files: a file not
// loaded now is an inactive service, and templates ("getty@.service"),
// which cannot be started as they are, are left out.
func servicesFrom(units []unitStatus, files []unitFile) []Service {
	byName := map[string]*Service{}
	for _, f := range files {
		name := filepath.Base(f.Path)
		if !strings.HasSuffix(name, ".service") || strings.HasSuffix(name, "@.service") {
			continue
		}
		byName[name] = &Service{Name: name, Active: "inactive", Sub: "dead", Boot: f.State}
	}
	for _, u := range units {
		if !strings.HasSuffix(u.Name, ".service") || strings.HasSuffix(u.Name, "@.service") {
			continue
		}
		s := byName[u.Name]
		if s == nil {
			if u.Load == "not-found" && u.Active == "inactive" {
				continue // only named by another unit
			}
			s = &Service{Name: u.Name}
			byName[u.Name] = s
		}
		s.Description, s.Load, s.Active, s.Sub = u.Description, u.Load, u.Active, u.Sub
	}
	out := make([]Service, 0, len(byName))
	for _, s := range byName {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func serviceScope(scope string) (string, error) {
	if scope != "system" && scope != "user" {
		return "", badRequest{`scope must be "system" or "user"`}
	}
	return scope, nil
}

func (w *Worker) listServices(ctx context.Context, scope string) ([]Service, bool, error) {
	src := w.services.source(scope)
	if src == nil {
		return []Service{}, false, nil
	}
	units, err := src.units(ctx)
	if err != nil {
		return nil, true, err
	}
	files, err := src.unitFiles(ctx)
	if err != nil {
		return nil, true, err
	}
	return servicesFrom(units, files), true, nil
}

func (w *Worker) handleServices(rw http.ResponseWriter, r *http.Request) {
	scope, err := serviceScope(r.URL.Query().Get("scope"))
	if err != nil {
		writeErr(rw, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	list, ok, err := w.listServices(ctx, scope)
	if err != nil {
		writeSystemdErr(rw, err)
		return
	}
	writeJSON(rw, http.StatusOK, map[string]any{"available": ok, "services": list})
}

// serviceSettle bounds the wait for a service to finish starting or
// stopping; systemd's own default start timeout is 90 s, but a service
// still starting after this is reported as such.
var serviceSettle = 30 * time.Second

// handleServiceAction starts, stops or restarts a service, waits until it
// has settled (or serviceSettle passed), and returns its state then.
func (w *Worker) handleServiceAction(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		Scope  string `json:"scope"`
		Name   string `json:"name"`
		Action string `json:"action"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(rw, err)
		return
	}
	scope, err := serviceScope(req.Scope)
	if err != nil {
		writeErr(rw, err)
		return
	}
	method := map[string]string{"start": "StartUnit", "stop": "StopUnit", "restart": "RestartUnit"}[req.Action]
	if method == "" {
		writeErr(rw, badRequest{"action must be start, stop or restart"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), authTimeout+time.Minute+serviceSettle)
	defer cancel()
	list, ok, err := w.listServices(ctx, scope)
	if err != nil {
		writeSystemdErr(rw, err)
		return
	}
	known := false
	for _, s := range list {
		known = known || s.Name == req.Name
	}
	if !ok || !known {
		writeErr(rw, badRequest{"no such service"})
		return
	}
	src := w.services.source(scope)
	// The user's own manager asks nobody; the system's asks polkit, which
	// can ask for a password only through Tilde's agent.
	interactive := scope == "system" && w.polkit.running()
	if err := src.call(ctx, method, interactive, req.Name, "replace"); err != nil {
		writeSystemdErr(rw, err)
		return
	}
	active, sub := waitSettled(ctx, src, req.Name)
	if scope == "system" {
		w.audit(r, protocol.AuditEvent{Event: "service_" + req.Action, Name: req.Name, Result: active})
	}
	writeJSON(rw, http.StatusOK, map[string]string{"active": active, "sub": sub})
}

// waitSettled follows a service until it is no longer starting, stopping
// or reloading.
func waitSettled(ctx context.Context, src systemdSource, name string) (string, string) {
	deadline := time.Now().Add(serviceSettle)
	for {
		active, sub, err := src.state(ctx, name)
		if err != nil {
			return "unknown", ""
		}
		switch active {
		case "activating", "deactivating", "reloading", "refreshing":
		default:
			return active, sub
		}
		if time.Now().After(deadline) {
			return active, sub
		}
		select {
		case <-ctx.Done():
			return active, sub
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// isUnknownMethod: a systemd older than 238, without the ...WithFlags methods.
func isUnknownMethod(err error) bool {
	var de dbus.Error
	return errors.As(err, &de) && de.Name == "org.freedesktop.DBus.Error.UnknownMethod"
}

func writeSystemdErr(rw http.ResponseWriter, err error) {
	var de dbus.Error
	if errors.As(err, &de) {
		switch de.Name {
		case "org.freedesktop.DBus.Error.AccessDenied", "org.freedesktop.DBus.Error.InteractiveAuthorizationRequired":
			writeJSON(rw, http.StatusForbidden, apiError{Error: "not allowed: this needs an administrator's password", Code: "not_authorized"})
			return
		case systemdName + ".NoSuchUnit", systemdName + ".LoadFailed":
			writeJSON(rw, http.StatusNotFound, apiError{Error: err.Error(), Code: "not_found"})
			return
		}
	}
	writeJSON(rw, http.StatusBadGateway, apiError{Error: err.Error(), Code: "systemd_error"})
}

// JournalEntry is one line of a service's journal.
type JournalEntry struct {
	// Time in Unix milliseconds.
	Time int64 `json:"time"`
	// Priority is syslog's: 0 emergency ... 3 error, 4 warning ... 7 debug.
	Priority int    `json:"priority"`
	Ident    string `json:"ident,omitempty"`
	PID      int    `json:"pid,omitempty"`
	Message  string `json:"message"`
}

// journalCommand is journalctl; tests replace it.
var journalCommand = "journalctl"

// journalLines is how far back the details pane reads.
const journalLines = 200

// limitedHint is what journalctl prints when the user may read only their
// own logs (only to a terminal, in recent versions).
const limitedHint = "not seeing messages from other users and the system"

// journalGroups may read the system journal: Debian's and Ubuntu's adm,
// systemd's own group, and Fedora's wheel.
var journalGroups = []string{"adm", "systemd-journal", "wheel"}

// readsSystemJournal: root, or in a group journald lets read every log.
func readsSystemJournal() bool {
	if os.Geteuid() == 0 {
		return true
	}
	gids, err := os.Getgroups()
	if err != nil {
		return false
	}
	gids = append(gids, os.Getegid())
	for _, gid := range gids {
		if g, err := user.LookupGroupId(strconv.Itoa(gid)); err == nil && slices.Contains(journalGroups, g.Name) {
			return true
		}
	}
	return false
}

// parseJournal reads journalctl's JSON lines. MESSAGE is a string, or an
// array of bytes when it is not valid UTF-8.
func parseJournal(out []byte) []JournalEntry {
	entries := []JournalEntry{}
	for _, line := range bytes.Split(out, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var raw map[string]json.RawMessage
		if json.Unmarshal(line, &raw) != nil {
			continue
		}
		str := func(k string) string {
			var s string
			if json.Unmarshal(raw[k], &s) == nil {
				return s
			}
			var b []byte
			var nums []int
			if json.Unmarshal(raw[k], &nums) == nil {
				for _, n := range nums {
					b = append(b, byte(n))
				}
				return strings.ToValidUTF8(string(b), "�")
			}
			return ""
		}
		us, _ := strconv.ParseInt(str("__REALTIME_TIMESTAMP"), 10, 64)
		prio := 6
		if p, err := strconv.Atoi(str("PRIORITY")); err == nil {
			prio = p
		}
		pid, _ := strconv.Atoi(str("_PID"))
		entries = append(entries, JournalEntry{
			Time: us / 1000, Priority: prio, Ident: str("SYSLOG_IDENTIFIER"), PID: pid, Message: str("MESSAGE"),
		})
	}
	return entries
}

// handleServiceJournal returns a service's last journal lines, read by
// journalctl as the user: journald decides what they may see (only their
// own logs unless they are in adm or systemd-journal), and `limited` says
// when it held some back.
func (w *Worker) handleServiceJournal(rw http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	scope, err := serviceScope(q.Get("scope"))
	if err != nil {
		writeErr(rw, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	name := q.Get("name")
	if err := w.knownService(ctx, scope, name); err != nil {
		writeErr(rw, err)
		return
	}
	args := []string{"--no-pager", "-o", "json", "-n", strconv.Itoa(journalLines), "-u", name}
	if scope == "user" {
		args = append([]string{"--user"}, args...)
	}
	cmd := exec.CommandContext(ctx, journalCommand, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	limited := strings.Contains(stderr.String(), limitedHint) || strings.Contains(stderr.String(), "insufficient permissions") ||
		scope == "system" && !readsSystemJournal()
	if err != nil && len(out) == 0 && !limited {
		writeJSON(rw, http.StatusBadGateway, apiError{Error: "journalctl: " + strings.TrimSpace(firstNonEmpty(stderr.String(), err.Error())), Code: "journal_error"})
		return
	}
	writeJSON(rw, http.StatusOK, map[string]any{"entries": parseJournal(out), "limited": limited})
}

// knownService checks that name is a service the manager lists.
func (w *Worker) knownService(ctx context.Context, scope, name string) error {
	list, ok, err := w.listServices(ctx, scope)
	if err != nil {
		return err
	}
	if ok {
		for _, s := range list {
			if s.Name == name {
				return nil
			}
		}
	}
	return badRequest{"no such service"}
}

// handleServiceBoot enables or disables a service at boot. It does not
// reload systemd afterwards, as systemctl does: the setting is on disk and
// the next boot reads it, and a reload is one more administrator's
// password from a web session.
//
// It uses the ...WithFlags methods, as systemctl does. systemd lets polkit
// ask for a password here only when the call says it may (unlike starting
// and stopping, which always may), so the flag must reach the bus
// (dbuscall.go).
func (w *Worker) handleServiceBoot(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		Scope   string `json:"scope"`
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(rw, err)
		return
	}
	scope, err := serviceScope(req.Scope)
	if err != nil {
		writeErr(rw, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), authTimeout+time.Minute)
	defer cancel()
	if err := w.knownService(ctx, scope, req.Name); err != nil {
		writeErr(rw, err)
		return
	}
	src := w.services.source(scope)
	interactive := scope == "system" && w.polkit.running()
	files := []string{req.Name}
	if req.Enabled {
		err = src.call(ctx, "EnableUnitFilesWithFlags", interactive, files, uint64(0))
		if isUnknownMethod(err) {
			err = src.call(ctx, "EnableUnitFiles", interactive, files, false, false)
		}
	} else {
		err = src.call(ctx, "DisableUnitFilesWithFlags", interactive, files, uint64(0))
		if isUnknownMethod(err) {
			err = src.call(ctx, "DisableUnitFiles", interactive, files, false)
		}
	}
	if err != nil {
		writeSystemdErr(rw, err)
		return
	}
	boot := ""
	if files, err := src.unitFiles(ctx); err == nil {
		for _, f := range files {
			if filepath.Base(f.Path) == req.Name {
				boot = f.State
			}
		}
	}
	if scope == "system" {
		ev := "service_disable"
		if req.Enabled {
			ev = "service_enable"
		}
		w.audit(r, protocol.AuditEvent{Event: ev, Name: req.Name, Result: boot})
	}
	writeJSON(rw, http.StatusOK, map[string]string{"boot": boot})
}
