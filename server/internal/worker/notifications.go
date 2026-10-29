package worker

import (
	"bytes"
	"errors"
	"html"
	"image"
	"image/png"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"

	"tilde/internal/buildinfo"
)

// Notifications (D70): the worker is the user's notification server, on
// the session bus, when no other program is, as on a headless server or a
// Pi; programs' notifications (notify-send, apt, browsers) then show in
// every browser. While a desktop at the machine has the name (Cinnamon's
// own notifications), the worker waits in the bus's queue for it and gets
// it if the desktop lets go; it allows replacement, so a desktop starting
// later takes the name back.

const (
	notifyName  = "org.freedesktop.Notifications"
	notifyPath  = dbus.ObjectPath("/org/freedesktop/Notifications")
	notifyIface = "org.freedesktop.Notifications"
)

// Why a notification closed, as the specification numbers them.
const (
	closedExpired   = 1
	closedDismissed = 2
	closedByCall    = 3
)

// Bounds on what a program may send: notifications come from anything
// the user runs, and every browser shows them.
const (
	maxNotifications = 50
	maxSummary       = 512
	maxBody          = 4096
	maxActions       = 8
	maxImageSide     = 512
	// defaultExpiry is for a notification that leaves it to the server
	// (expire_timeout -1); critical ones stay until dismissed.
	defaultExpiry = 6 * time.Second
	maxExpiry     = time.Hour
)

// Notification is one notification, as the browser shows it.
type Notification struct {
	ID  uint32 `json:"id"`
	App string `json:"app"`
	// Icon is an icon name; IconPath a picture file of the user's.
	Icon     string `json:"icon,omitempty"`
	IconPath string `json:"iconPath,omitempty"`
	// Image: a picture came with it (image-data), at /notifications/image.
	Image   bool                 `json:"image,omitempty"`
	Summary string               `json:"summary"`
	Body    string               `json:"body,omitempty"`
	Actions []NotificationAction `json:"actions,omitempty"`
	// Urgency: 0 low, 1 normal, 2 critical.
	Urgency int `json:"urgency"`
	// Time it came, in Unix milliseconds.
	Time int64 `json:"time"`
	// Open: shown as a toast, until it expires, is dismissed or the
	// program closes it; closed ones stay in the list.
	Open bool `json:"open"`

	resident, transient bool
	png                 []byte
	// gen tells a timer from before a replacement it should ignore.
	gen   int
	timer *time.Timer
}

// NotificationAction is a button: the key goes back to the program.
type NotificationAction struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

var (
	errNoNotification = errors.New("no such notification")
	errNoAction       = errors.New("the notification has no such action")
)

type notificationService struct {
	publish func()
	// expiry overrides defaultExpiry in tests.
	expiry time.Duration

	mu     sync.Mutex
	conn   *dbus.Conn
	owned  bool
	closed bool
	nextID uint32
	list   []*Notification // oldest first
}

func newNotificationService(publish func()) *notificationService {
	return &notificationService{publish: publish, expiry: defaultExpiry}
}

// start connects to the session bus at addr and asks for the name. Without
// a session bus, Tilde shows no notifications from programs.
func (s *notificationService) start(addr string) {
	if addr == "" {
		slog.Info("worker: no notifications: no session bus")
		return
	}
	conn, err := dbus.Connect(addr)
	if err != nil {
		slog.Info("worker: no notifications", "err", err)
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		conn.Close()
		return
	}
	s.conn = conn
	s.mu.Unlock()
	fail := func(err error) {
		slog.Info("worker: no notifications", "err", err)
		s.mu.Lock()
		s.conn = nil
		s.mu.Unlock()
		conn.Close()
	}
	if err := conn.Export(notifyObject{s}, notifyPath, notifyIface); err != nil {
		fail(err)
		return
	}
	if err := conn.Export(introspect.Introspectable(notifyIntrospection), notifyPath, "org.freedesktop.DBus.Introspectable"); err != nil {
		fail(err)
		return
	}
	// NameAcquired and NameLost come to this connection alone, before and
	// after the reply below; watch for them first.
	signals := make(chan *dbus.Signal, 16)
	conn.Signal(signals)
	go s.watchName(signals)
	reply, err := conn.RequestName(notifyName, dbus.NameFlagAllowReplacement)
	if err != nil {
		fail(err)
		return
	}
	if reply == dbus.RequestNameReplyPrimaryOwner {
		s.setOwned(true)
	} else {
		slog.Info("worker: another program shows notifications; waiting for it to stop")
	}
}

func (s *notificationService) watchName(signals <-chan *dbus.Signal) {
	for sig := range signals {
		if len(sig.Body) == 0 || sig.Body[0] != notifyName {
			continue
		}
		switch sig.Name {
		case "org.freedesktop.DBus.NameAcquired":
			s.setOwned(true)
		case "org.freedesktop.DBus.NameLost":
			s.setOwned(false)
		}
	}
}

func (s *notificationService) setOwned(on bool) {
	s.mu.Lock()
	changed := s.owned != on
	s.owned = on
	s.mu.Unlock()
	if changed {
		slog.Info("worker: notification server", "active", on)
		s.publish()
	}
}

func (s *notificationService) close() {
	s.mu.Lock()
	s.closed = true
	conn := s.conn
	s.conn, s.owned = nil, false
	for _, n := range s.list {
		if n.timer != nil {
			n.timer.Stop()
		}
	}
	s.mu.Unlock()
	if conn != nil {
		conn.Close()
	}
}

// active reports whether programs' notifications come here.
func (s *notificationService) active() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.owned
}

// snapshot is the list, newest first.
func (s *notificationService) snapshot() []Notification {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Notification, 0, len(s.list))
	for i := len(s.list) - 1; i >= 0; i-- {
		out = append(out, *s.list[i])
	}
	return out
}

func (s *notificationService) find(id uint32) *Notification {
	for _, n := range s.list {
		if n.ID == id {
			return n
		}
	}
	return nil
}

func (s *notificationService) image(id uint32) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n := s.find(id); n != nil {
		return n.png
	}
	return nil
}

// notify adds a notification, or replaces one still listed, and returns
// its id.
func (s *notificationService) notify(n *Notification, replaces uint32, expire int32) uint32 {
	s.mu.Lock()
	if old := s.find(replaces); replaces != 0 && old != nil {
		if old.timer != nil {
			old.timer.Stop()
		}
		n.ID, n.gen = old.ID, old.gen+1
		*old = *n
		n = old
	} else {
		s.nextID++
		if s.nextID == 0 { // 0 means "none" to programs
			s.nextID = 1
		}
		n.ID = s.nextID
		s.list = append(s.list, n)
		s.trim()
	}
	n.Open = true
	if d := s.expiryOf(n, expire); d > 0 {
		id, gen := n.ID, n.gen
		n.timer = time.AfterFunc(d, func() { s.expire(id, gen) })
	}
	id := n.ID
	s.mu.Unlock()
	s.publish()
	return id
}

// trim keeps the list to maxNotifications, dropping the oldest closed
// ones first.
func (s *notificationService) trim() {
	for len(s.list) > maxNotifications {
		i := slices.IndexFunc(s.list, func(n *Notification) bool { return !n.Open })
		if i < 0 {
			i = 0
		}
		if t := s.list[i].timer; t != nil {
			t.Stop()
		}
		s.list = slices.Delete(s.list, i, i+1)
	}
}

func (s *notificationService) expiryOf(n *Notification, expire int32) time.Duration {
	switch {
	case expire == 0:
		return 0
	case expire < 0:
		if n.Urgency == 2 {
			return 0
		}
		return s.expiry
	}
	return min(time.Duration(expire)*time.Millisecond, maxExpiry)
}

func (s *notificationService) expire(id uint32, gen int) {
	s.mu.Lock()
	n := s.find(id)
	if n == nil || n.gen != gen || !n.Open {
		s.mu.Unlock()
		return
	}
	s.closeLocked(n)
	conn := s.conn
	s.mu.Unlock()
	s.signalClosed(conn, id, closedExpired)
	s.publish()
}

// closeLocked ends n's toast; a transient one leaves the list too.
func (s *notificationService) closeLocked(n *Notification) {
	n.Open = false
	if n.timer != nil {
		n.timer.Stop()
		n.timer = nil
	}
	if n.transient {
		s.list = slices.DeleteFunc(s.list, func(m *Notification) bool { return m == n })
	}
}

// closeOpen closes notification id if it is open, telling its program why.
func (s *notificationService) closeOpen(id uint32, reason uint32) error {
	s.mu.Lock()
	n := s.find(id)
	if n == nil {
		s.mu.Unlock()
		return errNoNotification
	}
	if !n.Open {
		s.mu.Unlock()
		return nil
	}
	s.closeLocked(n)
	conn := s.conn
	s.mu.Unlock()
	s.signalClosed(conn, id, reason)
	s.publish()
	return nil
}

// invoke passes a clicked action to the program; the notification then
// closes, unless the program asked it to stay (resident).
func (s *notificationService) invoke(id uint32, key string) error {
	s.mu.Lock()
	n := s.find(id)
	if n == nil || !n.Open && !n.resident {
		s.mu.Unlock()
		return errNoNotification
	}
	if !slices.ContainsFunc(n.Actions, func(a NotificationAction) bool { return a.Key == key }) {
		s.mu.Unlock()
		return errNoAction
	}
	conn := s.conn
	closing := !n.resident && n.Open
	if closing {
		s.closeLocked(n)
	}
	s.mu.Unlock()
	if conn != nil {
		if err := conn.Emit(notifyPath, notifyIface+".ActionInvoked", id, key); err != nil {
			slog.Info("worker: notification action", "err", err)
		}
	}
	if closing {
		s.signalClosed(conn, id, closedDismissed)
	}
	s.publish()
	return nil
}

// clear removes notifications from the list (every one without ids),
// closing those still open.
func (s *notificationService) clear(ids []uint32) {
	s.mu.Lock()
	var gone []uint32
	s.list = slices.DeleteFunc(s.list, func(n *Notification) bool {
		if len(ids) > 0 && !slices.Contains(ids, n.ID) {
			return false
		}
		if n.Open {
			gone = append(gone, n.ID)
		}
		if n.timer != nil {
			n.timer.Stop()
		}
		return true
	})
	conn := s.conn
	s.mu.Unlock()
	for _, id := range gone {
		s.signalClosed(conn, id, closedDismissed)
	}
	s.publish()
}

func (s *notificationService) signalClosed(conn *dbus.Conn, id, reason uint32) {
	if conn == nil {
		return
	}
	if err := conn.Emit(notifyPath, notifyIface+".NotificationClosed", id, reason); err != nil {
		slog.Info("worker: notification closed signal", "err", err)
	}
}

// notifyObject is what the worker exports: godbus exports every exported
// method, so this type has only the specification's (version 1.2).
type notifyObject struct{ s *notificationService }

func (o notifyObject) GetCapabilities() ([]string, *dbus.Error) {
	// No "body-markup": bodies are shown as text; markup sent anyway is
	// removed.
	return []string{"actions", "body", "icon-static", "persistence"}, nil
}

func (o notifyObject) GetServerInformation() (string, string, string, string, *dbus.Error) {
	return "Tilde", "Tilde", buildinfo.Version, "1.2", nil
}

func (o notifyObject) CloseNotification(id uint32) *dbus.Error {
	// Unknown or already closed: nothing to do.
	_ = o.s.closeOpen(id, closedByCall)
	return nil
}

func (o notifyObject) Notify(app string, replaces uint32, icon, summary, body string, actions []string,
	hints map[string]dbus.Variant, expire int32) (uint32, *dbus.Error) {
	n := &Notification{
		App:     clip(app, 128),
		Summary: clip(summary, maxSummary),
		Body:    clip(plainText(body), maxBody),
		Urgency: 1,
		Time:    time.Now().UnixMilli(),
	}
	for i := 0; i+1 < len(actions) && len(n.Actions) < maxActions; i += 2 {
		n.Actions = append(n.Actions, NotificationAction{Key: clip(actions[i], 128), Label: clip(actions[i+1], 64)})
	}
	if u, ok := hints["urgency"].Value().(byte); ok && u <= 2 {
		n.Urgency = int(u)
	}
	n.resident, _ = hints["resident"].Value().(bool)
	n.transient, _ = hints["transient"].Value().(bool)
	// The specification's order: image-data, then image-path, then the
	// program's icon (with the old spellings of the hints).
	for _, k := range []string{"image-data", "image_data", "icon_data"} {
		if v, ok := hints[k]; ok {
			if b := imageDataPNG(v); b != nil {
				n.png, n.Image = b, true
				break
			}
		}
	}
	if !n.Image {
		pic, _ := hints["image-path"].Value().(string)
		if pic == "" {
			pic, _ = hints["image_path"].Value().(string)
		}
		if pic == "" {
			pic = icon
		}
		if pic == "" {
			// Often the program's icon name too.
			pic, _ = hints["desktop-entry"].Value().(string)
		}
		n.Icon, n.IconPath = iconOrPath(pic)
	}
	return o.s.notify(n, replaces, expire), nil
}

// iconOrPath tells an icon name from a picture: a file:// URI or an
// absolute path.
func iconOrPath(s string) (name, path string) {
	if u, err := url.Parse(s); err == nil && u.Scheme == "file" {
		s = u.Path
	}
	switch {
	case s == "":
	case filepath.IsAbs(s):
		path = filepath.Clean(s)
	case validIconName(s):
		name = s
	}
	return name, path
}

var iconNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$`)

func validIconName(s string) bool { return iconNameRe.MatchString(s) }

// plainText removes the markup some programs send even to a server that
// does not offer it (<b>, <i>, <a href>, <img>), keeping the text.
var markupTag = regexp.MustCompile(`</?[A-Za-z][^<>]*>`)

func plainText(s string) string {
	if !strings.ContainsAny(s, "<&") {
		return s
	}
	return html.UnescapeString(markupTag.ReplaceAllString(s, ""))
}

// clip cuts s to at most n bytes, on a character boundary.
func clip(s string, n int) string {
	s = strings.ToValidUTF8(s, "�")
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// imageDataPNG turns the image-data hint, raw pixels as (iiibiiay): width,
// height, row stride, alpha, bits per sample, channels, data, into a PNG.
// Anything else, or a picture larger than maxImageSide, gives nil.
func imageDataPNG(v dbus.Variant) []byte {
	f, ok := v.Value().([]any)
	if !ok || len(f) != 7 {
		return nil
	}
	w, ok1 := f[0].(int32)
	h, ok2 := f[1].(int32)
	stride, ok3 := f[2].(int32)
	alpha, ok4 := f[3].(bool)
	bits, ok5 := f[4].(int32)
	channels, ok6 := f[5].(int32)
	data, ok7 := f[6].([]byte)
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || !ok6 || !ok7 {
		return nil
	}
	if w < 1 || h < 1 || w > maxImageSide || h > maxImageSide || bits != 8 {
		return nil
	}
	want := int32(3)
	if alpha {
		want = 4
	}
	if channels != want || stride < w*channels || int64(len(data)) < int64(stride)*int64(h-1)+int64(w*channels) {
		return nil
	}
	img := image.NewNRGBA(image.Rect(0, 0, int(w), int(h)))
	for y := range int(h) {
		row := data[y*int(stride):]
		for x := range int(w) {
			p := row[x*int(channels):]
			o := img.PixOffset(x, y)
			img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = p[0], p[1], p[2], 255
			if alpha {
				img.Pix[o+3] = p[3]
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}

const notifyIntrospection = `<node>
  <interface name="org.freedesktop.Notifications">
    <method name="GetCapabilities"><arg type="as" direction="out"/></method>
    <method name="Notify">
      <arg type="s" direction="in"/><arg type="u" direction="in"/><arg type="s" direction="in"/>
      <arg type="s" direction="in"/><arg type="s" direction="in"/><arg type="as" direction="in"/>
      <arg type="a{sv}" direction="in"/><arg type="i" direction="in"/><arg type="u" direction="out"/>
    </method>
    <method name="CloseNotification"><arg type="u" direction="in"/></method>
    <method name="GetServerInformation">
      <arg type="s" direction="out"/><arg type="s" direction="out"/>
      <arg type="s" direction="out"/><arg type="s" direction="out"/>
    </method>
    <signal name="NotificationClosed"><arg type="u"/><arg type="u"/></signal>
    <signal name="ActionInvoked"><arg type="u"/><arg type="s"/></signal>
  </interface>` + introspect.IntrospectDeclarationString + `</node>`

// handleNotifications lists the notifications; `active`: programs'
// notifications come to Tilde (no other program has the name).
func (w *Worker) handleNotifications(rw http.ResponseWriter, r *http.Request) {
	writeJSON(rw, http.StatusOK, map[string]any{
		"active":        w.notifications.active(),
		"notifications": w.notifications.snapshot(),
	})
}

// handleNotificationAction is a click on one of a notification's buttons,
// or on the notification itself (the "default" action).
func (w *Worker) handleNotificationAction(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		ID  uint32 `json:"id"`
		Key string `json:"key"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(rw, err)
		return
	}
	switch err := w.notifications.invoke(req.ID, req.Key); {
	case errors.Is(err, errNoNotification), errors.Is(err, errNoAction):
		writeJSON(rw, http.StatusNotFound, apiError{Error: err.Error(), Code: "not_found"})
	case err != nil:
		writeErr(rw, err)
	default:
		rw.WriteHeader(http.StatusNoContent)
	}
}

// handleNotificationClose dismisses a notification's toast; it stays in
// the list.
func (w *Worker) handleNotificationClose(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		ID uint32 `json:"id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(rw, err)
		return
	}
	if err := w.notifications.closeOpen(req.ID, closedDismissed); err != nil {
		writeJSON(rw, http.StatusNotFound, apiError{Error: err.Error(), Code: "not_found"})
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

// handleNotificationClear removes notifications from the list: those
// given, or all.
func (w *Worker) handleNotificationClear(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []uint32 `json:"ids"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(rw, err)
		return
	}
	w.notifications.clear(req.IDs)
	rw.WriteHeader(http.StatusNoContent)
}

// handleNotificationImage serves a picture a program sent with a
// notification, made into a PNG here.
func (w *Worker) handleNotificationImage(rw http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.URL.Query().Get("id"), 10, 32)
	var b []byte
	if err == nil {
		b = w.notifications.image(uint32(id))
	}
	if b == nil {
		writeJSON(rw, http.StatusNotFound, apiError{Error: errNoNotification.Error(), Code: "not_found"})
		return
	}
	h := rw.Header()
	h.Set("Content-Type", "image/png")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	h.Set("Cache-Control", "no-cache") // a replacement keeps the id
	rw.Write(b)
}
