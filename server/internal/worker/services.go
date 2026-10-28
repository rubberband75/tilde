package worker

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
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
	var flags dbus.Flags
	if interactive {
		flags = dbus.FlagAllowInteractiveAuthorization
	}
	return b.manager().CallWithContext(ctx, systemdManager+"."+method, flags, args...).Err
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
