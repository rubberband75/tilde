package worker

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"image/png"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// privateBus starts a dbus-daemon of its own for the test, which nothing
// else is on (no service activation either), and returns its address.
func privateBus(t *testing.T) string {
	t.Helper()
	daemon, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skip("no dbus-daemon")
	}
	dir := t.TempDir()
	conf := filepath.Join(dir, "bus.conf")
	os.WriteFile(conf, []byte(`<busconfig>
  <type>session</type>
  <listen>unix:path=`+filepath.Join(dir, "bus")+`</listen>
  <auth>EXTERNAL</auth>
  <policy context="default">
    <allow send_destination="*" eavesdrop="true"/>
    <allow eavesdrop="true"/>
    <allow own="*"/>
  </policy>
</busconfig>`), 0o600)
	cmd := exec.Command(daemon, "--config-file="+conf, "--nofork", "--print-address=1")
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatalf("dbus-daemon: %v", err)
	}
	return strings.TrimSpace(line)
}

func busClient(t *testing.T, addr string) *dbus.Conn {
	t.Helper()
	c, err := dbus.Connect(addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// notificationsWorker is a worker serving notifications on its own bus.
func notificationsWorker(t *testing.T) (*Worker, string, *atomic.Int32) {
	t.Helper()
	addr := privateBus(t)
	w := driveWorker(t, nil)
	events := new(atomic.Int32)
	w.notifications = newNotificationService(func() { events.Add(1) })
	w.notifications.start(addr)
	t.Cleanup(w.notifications.close)
	if !w.notifications.active() {
		t.Fatal("the worker did not get the name")
	}
	return w, addr, events
}

func sendNotify(t *testing.T, c *dbus.Conn, replaces uint32, summary, body string, actions []string, hints map[string]dbus.Variant, expire int32) uint32 {
	t.Helper()
	var id uint32
	err := c.Object(notifyName, notifyPath).Call(notifyIface+".Notify", 0,
		"Test App", replaces, "dialog-information", summary, body, actions, hints, expire).Store(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func listNotifications(t *testing.T, w *Worker) (bool, []Notification) {
	t.Helper()
	rec := serve(t, w, "GET", "/api/v1/notifications", "")
	var res struct {
		Active        bool
		Notifications []Notification
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &res) != nil {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	return res.Active, res.Notifications
}

// signals collects the server's signals on a client connection.
func signals(t *testing.T, c *dbus.Conn) <-chan *dbus.Signal {
	t.Helper()
	if err := c.AddMatchSignal(dbus.WithMatchInterface(notifyIface)); err != nil {
		t.Fatal(err)
	}
	ch := make(chan *dbus.Signal, 16)
	c.Signal(ch)
	return ch
}

func nextSignal(t *testing.T, ch <-chan *dbus.Signal) string {
	t.Helper()
	for {
		select {
		case s := <-ch:
			if strings.HasPrefix(s.Name, notifyIface+".") {
				return strings.TrimPrefix(s.Name, notifyIface+".") + fmt.Sprint(s.Body)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no signal")
			return ""
		}
	}
}

// TestNotifications: a program's notification shows with its actions;
// clicking one tells the program and closes it; dismissing, the program
// closing it and expiring each send NotificationClosed with their reason.
func TestNotifications(t *testing.T) {
	w, addr, events := notificationsWorker(t)
	c := busClient(t, addr)
	sigs := signals(t, c)

	var caps []string
	if err := c.Object(notifyName, notifyPath).Call(notifyIface+".GetCapabilities", 0).Store(&caps); err != nil ||
		!strings.Contains(strings.Join(caps, " "), "actions") {
		t.Fatalf("capabilities %v %v", caps, err)
	}
	id := sendNotify(t, c, 0, "Backup done", "<b>12 files</b> &amp; 3 folders",
		[]string{"default", "Open", "undo", "Undo"}, map[string]dbus.Variant{"urgency": dbus.MakeVariant(byte(2))}, -1)
	active, list := listNotifications(t, w)
	want := Notification{ID: id, App: "Test App", Icon: "dialog-information", Summary: "Backup done",
		Body: "12 files & 3 folders", Urgency: 2, Open: true,
		Actions: []NotificationAction{{"default", "Open"}, {"undo", "Undo"}}}
	if len(list) != 1 {
		t.Fatalf("list %+v", list)
	}
	got := list[0]
	got.Time = 0
	if !active || fmt.Sprint(got) != fmt.Sprint(want) || events.Load() == 0 {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}

	if rec := serve(t, w, "POST", "/api/v1/notifications/action", fmt.Sprintf(`{"id":%d,"key":"nope"}`, id)); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown action: %d", rec.Code)
	}
	if rec := serve(t, w, "POST", "/api/v1/notifications/action", fmt.Sprintf(`{"id":%d,"key":"undo"}`, id)); rec.Code != http.StatusNoContent {
		t.Fatalf("action: %d %s", rec.Code, rec.Body)
	}
	if s := nextSignal(t, sigs); s != fmt.Sprintf("ActionInvoked[%d undo]", id) {
		t.Fatalf("signal %s", s)
	}
	if s := nextSignal(t, sigs); s != fmt.Sprintf("NotificationClosed[%d 2]", id) {
		t.Fatalf("signal %s", s)
	}
	// Closed, still listed.
	if _, list = listNotifications(t, w); len(list) != 1 || list[0].Open {
		t.Fatalf("after the action: %+v", list)
	}

	// The program closes the next one; the one after expires.
	id2 := sendNotify(t, c, 0, "Two", "", nil, nil, 0)
	if err := c.Object(notifyName, notifyPath).Call(notifyIface+".CloseNotification", 0, id2).Err; err != nil {
		t.Fatal(err)
	}
	if s := nextSignal(t, sigs); s != fmt.Sprintf("NotificationClosed[%d 3]", id2) {
		t.Fatalf("signal %s", s)
	}
	id3 := sendNotify(t, c, 0, "Three", "", nil, nil, 50)
	if s := nextSignal(t, sigs); s != fmt.Sprintf("NotificationClosed[%d 1]", id3) {
		t.Fatalf("signal %s", s)
	}
	id4 := sendNotify(t, c, 0, "Four", "", nil, nil, 0)
	if rec := serve(t, w, "POST", "/api/v1/notifications/close", fmt.Sprintf(`{"id":%d}`, id4)); rec.Code != http.StatusNoContent {
		t.Fatalf("close: %d", rec.Code)
	}
	if s := nextSignal(t, sigs); s != fmt.Sprintf("NotificationClosed[%d 2]", id4) {
		t.Fatalf("signal %s", s)
	}
	if _, list = listNotifications(t, w); len(list) != 4 || list[0].ID != id4 || list[3].ID != id {
		t.Fatalf("newest first: %+v", list)
	}

	// Clearing removes them; an open one is closed first.
	id5 := sendNotify(t, c, 0, "Five", "", nil, nil, 0)
	if rec := serve(t, w, "POST", "/api/v1/notifications/clear", `{}`); rec.Code != http.StatusNoContent {
		t.Fatalf("clear: %d", rec.Code)
	}
	if s := nextSignal(t, sigs); s != fmt.Sprintf("NotificationClosed[%d 2]", id5) {
		t.Fatalf("signal %s", s)
	}
	if _, list = listNotifications(t, w); len(list) != 0 {
		t.Fatalf("cleared: %+v", list)
	}
}

// TestNotificationReplaceAndLimits: replaces_id updates in place and
// restarts the toast; hints for pictures, transient notifications, and
// the bounds on what a program may send.
func TestNotificationReplaceAndLimits(t *testing.T) {
	w, addr, _ := notificationsWorker(t)
	c := busClient(t, addr)

	id := sendNotify(t, c, 0, "Downloading", "10%", nil, nil, 0)
	if again := sendNotify(t, c, id, "Downloading", "90%", nil, nil, 0); again != id {
		t.Fatalf("replaced as %d, not %d", again, id)
	}
	if _, list := listNotifications(t, w); len(list) != 1 || list[0].Body != "90%" {
		t.Fatalf("replace: %+v", list)
	}

	// A 2×1 picture, red then translucent blue, as (iiibiiay).
	type imageData struct {
		W, H, Stride int32
		Alpha        bool
		Bits, Chans  int32
		Data         []byte
	}
	pixels := dbus.MakeVariant(imageData{2, 1, 8, true, 8, 4, []byte{255, 0, 0, 255, 0, 0, 255, 128}})
	pid := sendNotify(t, c, 0, "Picture", "", nil, map[string]dbus.Variant{"image-data": pixels}, 0)
	rec := serve(t, w, "GET", fmt.Sprintf("/api/v1/notifications/image?id=%d", pid), "")
	img, err := png.Decode(bytes.NewReader(rec.Body.Bytes()))
	if rec.Code != http.StatusOK || err != nil || img.Bounds().Dx() != 2 {
		t.Fatalf("image: %d %v", rec.Code, err)
	}
	if r, _, b, a := img.At(1, 0).RGBA(); r != 0 || b == 0 || a>>8 != 128 {
		t.Fatalf("pixel %v", img.At(1, 0))
	}
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatal("the picture is served without the sandbox")
	}
	path := sendNotify(t, c, 0, "Path", "", nil, map[string]dbus.Variant{"image-path": dbus.MakeVariant("file:///tmp/a%20b.png")}, 0)
	bad := sendNotify(t, c, 0, "Bad icon", "", nil, map[string]dbus.Variant{"image-path": dbus.MakeVariant("../../etc/x\"y")}, 0)
	short := sendNotify(t, c, 0, "Short", "", nil, map[string]dbus.Variant{"transient": dbus.MakeVariant(true)}, 0)
	if err := c.Object(notifyName, notifyPath).Call(notifyIface+".CloseNotification", 0, short).Err; err != nil {
		t.Fatal(err)
	}
	_, list := listNotifications(t, w)
	byID := map[uint32]Notification{}
	for _, n := range list {
		byID[n.ID] = n
	}
	if !byID[pid].Image || byID[path].IconPath != "/tmp/a b.png" || byID[bad].Icon != "" || byID[bad].IconPath != "" {
		t.Fatalf("pictures: %+v", list)
	}
	if _, ok := byID[short]; ok {
		t.Fatal("a transient notification stayed listed")
	}

	long := strings.Repeat("é", maxBody)
	actions := []string{}
	for i := range 20 {
		actions = append(actions, fmt.Sprint("k", i), "label")
	}
	big := sendNotify(t, c, 0, long, long, actions, nil, 0)
	_, list = listNotifications(t, w)
	if n := list[0]; n.ID != big || len(n.Summary) > maxSummary+len("…") || len(n.Body) > maxBody+len("…") ||
		len(n.Actions) != maxActions || !strings.HasSuffix(n.Body, "é…") {
		t.Fatalf("bounds: %d %d %d", len(n.Summary), len(n.Body), len(n.Actions))
	}
	for range maxNotifications + 5 {
		sendNotify(t, c, 0, "Many", "", nil, nil, 0)
	}
	if _, list = listNotifications(t, w); len(list) != maxNotifications {
		t.Fatalf("kept %d", len(list))
	}
}

// TestNotificationsWaitForTheDesktop: while another program has the name,
// the worker waits in the queue, and takes it when that one leaves.
func TestNotificationsWaitForTheDesktop(t *testing.T) {
	addr := privateBus(t)
	desktop := busClient(t, addr)
	if reply, err := desktop.RequestName(notifyName, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("desktop: %v %v", reply, err)
	}
	w := driveWorker(t, nil)
	events := make(chan struct{}, 4)
	w.notifications = newNotificationService(func() { events <- struct{}{} })
	w.notifications.start(addr)
	t.Cleanup(w.notifications.close)
	if active, _ := listNotifications(t, w); active {
		t.Fatal("took the name from the desktop")
	}
	if _, err := desktop.ReleaseName(notifyName); err != nil {
		t.Fatal(err)
	}
	select {
	case <-events:
	case <-time.After(5 * time.Second):
		t.Fatal("no event when the name came")
	}
	if active, _ := listNotifications(t, w); !active {
		t.Fatal("did not take the name when the desktop let go")
	}
	// A desktop starting later takes it back.
	if reply, err := desktop.RequestName(notifyName, dbus.NameFlagReplaceExisting); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("desktop again: %v %v", reply, err)
	}
	select {
	case <-events:
	case <-time.After(5 * time.Second):
		t.Fatal("no event when the name went")
	}
	if active, _ := listNotifications(t, w); active {
		t.Fatal("kept the name from the desktop")
	}
}

func TestPlainText(t *testing.T) {
	for in, want := range map[string]string{
		"plain":                       "plain",
		"a < b":                       "a < b",
		`<a href="x">link</a> &lt;3`:  "link <3",
		"<img src='x' alt='pic'/>end": "end",
		"5 &gt; 4 &amp; <i>yes</i>":   "5 > 4 & yes",
	} {
		if got := plainText(in); got != want {
			t.Errorf("plainText(%q) = %q, want %q", in, got, want)
		}
	}
}
