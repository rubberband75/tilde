package worker

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/godbus/dbus/v5"
)

// The worker's logind session (D41). Tilde's lock becomes the session's
// LockedHint, so "loginctl show-session" tells the truth, and "loginctl
// lock-session" (or lock-sessions) locks every browser of the session.
// logind refuses IdleHint for non-graphical sessions such as Tilde's "web"
// one, so idle is not reported, and its Unlock request is ignored: only
// the password unlocks a browser.

// logindSession is what the worker needs from logind; tests replace it.
type logindSession interface {
	// setLocked sets the session's LockedHint.
	setLocked(ctx context.Context, locked bool) error
	// locks receives a value each time logind asks the session to lock.
	locks() <-chan struct{}
	close()
}

const (
	busDaemon    = "org.freedesktop.DBus"
	logindName   = "org.freedesktop.login1"
	logindPath   = dbus.ObjectPath("/org/freedesktop/login1")
	sessionIface = "org.freedesktop.login1.Session"
)

// logindConn holds the worker's logind session. Connecting happens after
// the worker's hello, so a slow system bus never holds up the start of a
// session; ready is closed once it is done, and s (nil when there is
// nothing to follow) may be read from then on.
type logindConn struct {
	id    string
	ready chan struct{}
	s     logindSession
}

// readyLogind holds a session known up front (tests), or none.
func readyLogind(s logindSession) *logindConn {
	c := &logindConn{ready: make(chan struct{}), s: s}
	close(c.ready)
	return c
}

// connect follows the session named id; Run calls it after the hello.
func (c *logindConn) connect() {
	defer close(c.ready)
	s, err := dialLogind(c.id)
	if err != nil {
		slog.Info("worker: logind does not see the lock", "session", c.id, "err", err)
		return
	}
	c.s = s
}

// wait returns the session once connecting is done (nil if there is none
// to follow), or ctx's error if that takes too long.
func (c *logindConn) wait(ctx context.Context) (logindSession, error) {
	select {
	case <-c.ready:
		return c.s, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// busLogind is the worker's session on the system bus.
type busLogind struct {
	conn *dbus.Conn
	obj  dbus.BusObject
	lock chan struct{}
}

// dialLogind follows logind session id, which pam_systemd gave the worker
// in XDG_SESSION_ID, if it is a "web" session of this user. When the
// server was started from inside another session (sudo in a desktop
// terminal), pam_systemd joins that session instead of opening a web one,
// and Tilde must leave it alone.
func dialLogind(id string) (logindSession, error) {
	if id == "" {
		return nil, fmt.Errorf("no XDG_SESSION_ID")
	}
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, err
	}
	l, err := followSession(conn, id)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return l, nil
}

func followSession(conn *dbus.Conn, id string) (*busLogind, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var path dbus.ObjectPath
	if err := conn.Object(logindName, logindPath).CallWithContext(ctx, "org.freedesktop.login1.Manager.GetSession", 0, id).Store(&path); err != nil {
		return nil, err
	}
	obj := conn.Object(logindName, path)
	var typ, user dbus.Variant
	if err := obj.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, sessionIface, "Type").Store(&typ); err != nil {
		return nil, err
	}
	if err := obj.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, sessionIface, "User").Store(&user); err != nil {
		return nil, err
	}
	// User is (uo): the uid and the user's object path.
	fields, _ := user.Value().([]any)
	uid, ok := firstOf[uint32](fields)
	if typ.Value() != "web" || !ok || int(uid) != os.Getuid() {
		return nil, fmt.Errorf("session %s is a %v session of uid %d, not this user's web session", id, typ.Value(), uid)
	}

	// logind's bus name, to tell its signals from anyone else's.
	w := &lockWatcher{path: path}
	if err := conn.BusObject().CallWithContext(ctx, busDaemon+".GetNameOwner", 0, logindName).Store(&w.owner); err != nil {
		return nil, err
	}
	if err := conn.AddMatchSignalContext(ctx, dbus.WithMatchSender(busDaemon), dbus.WithMatchInterface(busDaemon),
		dbus.WithMatchMember("NameOwnerChanged"), dbus.WithMatchArg(0, logindName)); err != nil {
		return nil, err
	}
	if err := conn.AddMatchSignalContext(ctx, dbus.WithMatchSender(logindName), dbus.WithMatchObjectPath(path),
		dbus.WithMatchInterface(sessionIface), dbus.WithMatchMember("Lock")); err != nil {
		return nil, err
	}

	l := &busLogind{conn: conn, obj: obj, lock: make(chan struct{}, 1)}
	signals := make(chan *dbus.Signal, 16)
	conn.Signal(signals)
	go func() {
		// Ends when the connection closes (Shutdown).
		for sig := range signals {
			if w.isLock(sig) {
				slog.Info("worker: logind asked to lock the session")
				select {
				case l.lock <- struct{}{}:
				default: // one pending request is enough
				}
			}
		}
	}()
	return l, nil
}

func firstOf[T any](values []any) (T, bool) {
	var zero T
	if len(values) == 0 {
		return zero, false
	}
	v, ok := values[0].(T)
	return v, ok
}

// lockWatcher picks logind's Lock requests for the session out of the
// signals the worker's connection receives. Match rules only filter
// broadcasts: any user on the system bus can send a signal straight to the
// worker, so a Lock counts only from the bus name logind owns. When logind
// restarts, the bus daemon (whose name no peer can use) says so.
type lockWatcher struct {
	path  dbus.ObjectPath
	owner string // logind's unique bus name
}

func (w *lockWatcher) isLock(sig *dbus.Signal) bool {
	switch sig.Name {
	case busDaemon + ".NameOwnerChanged":
		if sig.Sender == busDaemon && len(sig.Body) == 3 && sig.Body[0] == logindName {
			w.owner, _ = sig.Body[2].(string)
		}
	case sessionIface + ".Lock":
		return w.owner != "" && sig.Sender == w.owner && sig.Path == w.path
	}
	return false
}

func (l *busLogind) setLocked(ctx context.Context, locked bool) error {
	return l.obj.CallWithContext(ctx, sessionIface+".SetLockedHint", 0, locked).Err
}

func (l *busLogind) locks() <-chan struct{} { return l.lock }

func (l *busLogind) close() { l.conn.Close() }
