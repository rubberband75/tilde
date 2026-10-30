package worker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestExecArgs(t *testing.T) {
	for in, want := range map[string][]string{
		"code --unity-launch %F":                 {"code", "--unity-launch"},
		`"/opt/My App/app" --name="a b" %u`:      {"/opt/My App/app", "--name=a b"},
		`sh -c "echo \"hi\" 100%%"`:              {"sh", "-c", `echo "hi" 100%`},
		"baobab":                                 {"baobab"},
		`env  GDK_BACKEND=x11   gimp-2.10 %U %i`: {"env", "GDK_BACKEND=x11", "gimp-2.10"},
		`app --file=%f "%u" --name x`:            {"app", "--name", "x"},
	} {
		got, err := execArgs(in)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("execArgs(%q) = %q %v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", `"open`, "%F"} {
		if _, err := execArgs(bad); err == nil {
			t.Errorf("execArgs(%q) passed", bad)
		}
	}
	for cats, want := range map[string]string{
		"GNOME;GTK;System;Filesystem;": "System Tools", "Utility;TextEditor;": "Accessories",
		"Development;IDE;": "Programming", "AudioVideo;Player;": "Sound & Video", "GTK;Settings;System;": "Preferences", "": "Other",
	} {
		if got := menuCategory(cats); got != want {
			t.Errorf("menuCategory(%q) = %q, want %q", cats, got, want)
		}
	}
}

func writeDesktop(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, "applications", rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte("[Desktop Entry]\n"+body+"\n[Desktop Action new]\nName=Not this\nExec=nope\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestListHostApps: apps from the user's and the system's applications/,
// the user's first; what is hidden, for one desktop only, a terminal's, or
// not installed is left out; icons are found in hicolor.
func TestListHostApps(t *testing.T) {
	user, sys := t.TempDir(), t.TempDir()
	writeDesktop(t, sys, "org.gnome.baobab.desktop", "Type=Application\nName=Disk Usage Analyzer\nComment=Check folder sizes\nExec=baobab %U\nIcon=org.gnome.baobab\nCategories=GTK;GNOME;System;Filesystem;")
	writeDesktop(t, sys, "code.desktop", "Type=Application\nName=Visual Studio Code\nExec=/usr/share/code/code %F\nIcon=/usr/share/pixmaps/vscode.png\nCategories=Development;")
	writeDesktop(t, user, "code.desktop", "Type=Application\nName=My Code\nExec=code\nCategories=Development;")
	writeDesktop(t, sys, "kde/kate.desktop", "Type=Application\nName=Kate\nExec=kate\nCategories=Utility;")
	writeDesktop(t, sys, "hidden.desktop", "Type=Application\nName=Hidden\nExec=x\nNoDisplay=true")
	writeDesktop(t, sys, "cinnamon-only.desktop", "Type=Application\nName=Cinnamon Settings\nExec=cinnamon-settings\nOnlyShowIn=X-Cinnamon;")
	writeDesktop(t, sys, "htop.desktop", "Type=Application\nName=Htop\nExec=htop\nTerminal=true")
	writeDesktop(t, sys, "missing.desktop", "Type=Application\nName=Missing\nExec=nothing-here\nTryExec=/nonexistent/tool")
	writeDesktop(t, sys, "link.desktop", "Type=Link\nName=A link\nURL=https://example.com")
	icon := filepath.Join(sys, "icons", "hicolor", "scalable", "apps", "org.gnome.baobab.svg")
	os.MkdirAll(filepath.Dir(icon), 0o755)
	os.WriteFile(icon, []byte("<svg/>"), 0o644)

	var got []string
	apps := listHostApps([]string{user, sys})
	for _, a := range apps {
		got = append(got, fmt.Sprintf("%s|%s|%s|%s|%v", a.ID, a.Name, a.Category, a.IconPath, a.exec))
	}
	want := []string{
		"org.gnome.baobab|Disk Usage Analyzer|System Tools|" + icon + "|[baobab]",
		"kde-kate|Kate|Accessories||[kate]",
		"code|My Code|Programming||[code]",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// fakeXpra stands in for xpra: "start --bind=S,..." says display 7 on
// fd 3 and listens on S until stopped, echoing what each connection
// sends; everything is logged. systemd-run is missing, so nothing goes in
// a slice.
func fakeXpra(t *testing.T) (log string) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3 for the fake xpra")
	}
	dir := t.TempDir()
	log = filepath.Join(dir, "log")
	script := filepath.Join(dir, "xpra")
	os.WriteFile(script, []byte(`#!/bin/sh
echo "$@" >> `+log+`
case "$1" in
start)
  for a; do case "$a" in --bind=*) sock="${a#--bind=}"; sock="${sock%%,*}";; esac; done
  echo 7 >&3
  exec 3>&-
  mkdir -p "$XDG_RUNTIME_DIR/xpra/7/pulse" && touch "$XDG_RUNTIME_DIR/xpra/7/pulse/native"
  exec python3 -c 'import socket,sys,threading
def echo(c):
    while True:
        b=c.recv(65536)
        if not b: break
        c.sendall(b)
    c.close()
s=socket.socket(socket.AF_UNIX); s.bind(sys.argv[1]); s.listen(8)
while True:
    c,_=s.accept(); threading.Thread(target=echo,args=(c,),daemon=True).start()' "$sock";;
stop) pkill -f "socket.AF_UNIX" -P $PPID 2>/dev/null; exit 0;;
esac
`), 0o755)
	www := filepath.Join(dir, "index.html")
	os.WriteFile(www, []byte("<html>"), 0o644)
	oldCmd, oldWWW, oldRun := xpraCommand, xpraWWW, systemdRun
	xpraCommand, xpraWWW, systemdRun = script, []string{www}, filepath.Join(dir, "no-systemd-run")
	t.Cleanup(func() { xpraCommand, xpraWWW, systemdRun = oldCmd, oldWWW, oldRun })
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	return log
}

// TestHostAppsLaunch: the session starts when the desktop asks, not
// because something connects; launching starts it once, on a socket in
// the runtime folder, and runs the app itself on its display, with its
// argv as is; without Xpra, the menu lists no host apps.
func TestHostAppsLaunch(t *testing.T) {
	log := fakeXpra(t)
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("XDG_DATA_DIRS", t.TempDir())
	out := filepath.Join(t.TempDir(), "ran")
	app := filepath.Join(t.TempDir(), "my app")
	os.WriteFile(app, []byte("#!/bin/sh\necho \"$DISPLAY ${PULSE_SERVER##*/xpra/} $*\" >> '"+out+"'\n"), 0o755)
	writeDesktop(t, data, "eyes.desktop", "Type=Application\nName=Eyes\nExec=\""+app+"\" -geometry \"1x1 two\" %U\nCategories=Utility;")

	w := driveWorker(t, nil)
	t.Cleanup(w.xpra.stop)
	rec := serve(t, w, "GET", "/api/v1/host-apps", "")
	var res struct {
		Available bool
		Apps      []HostApp
	}
	json.Unmarshal(rec.Body.Bytes(), &res)
	if !res.Available || len(res.Apps) != 1 || res.Apps[0].ID != "eyes" {
		t.Fatalf("list: %s", rec.Body)
	}
	// A connection does not start it.
	if _, err := w.xpra.dial(t.Context()); err != errXpraNotRunning {
		t.Fatalf("dial before the session: %v", err)
	}
	for range 2 {
		if rec := serve(t, w, "POST", "/api/v1/host-apps/launch", `{"id":"eyes"}`); rec.Code != http.StatusNoContent {
			t.Fatalf("launch: %d %s", rec.Code, rec.Body)
		}
	}
	if rec := serve(t, w, "POST", "/api/v1/host-apps/session", ``); rec.Code != http.StatusNoContent {
		t.Fatalf("session: %d %s", rec.Code, rec.Body)
	}
	if rec := serve(t, w, "POST", "/api/v1/host-apps/launch", `{"id":"nope"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown app: %d", rec.Code)
	}
	if c, err := w.xpra.dial(t.Context()); err != nil {
		t.Fatalf("dial: %v", err)
	} else {
		c.Close()
	}
	deadline := time.Now().Add(5 * time.Second)
	var ran []byte
	for time.Now().Before(deadline) {
		if ran, _ = os.ReadFile(out); strings.Count(string(ran), "\n") == 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if string(ran) != ":7 7/pulse/native -geometry 1x1 two\n:7 7/pulse/native -geometry 1x1 two\n" {
		t.Fatalf("the app ran as %q", ran)
	}
	b, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	sock := filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "tilde", "xpra.sock")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "start --bind="+sock+",auth=peercred --html=on --daemon=no") ||
		!strings.Contains(lines[0], "--start-new-commands=no") {
		t.Fatalf("xpra was run as:\n%s", b)
	}
	if st, err := os.Stat(filepath.Dir(sock)); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("socket folder: %v %v", st, err)
	}

	// The session ends with the worker, and does not start again.
	w.xpra.stop()
	if rec := serve(t, w, "POST", "/api/v1/host-apps/session", ``); rec.Code != http.StatusBadGateway {
		t.Fatalf("after stop: %d", rec.Code)
	}

	xpraCommand = filepath.Join(t.TempDir(), "missing")
	rec = serve(t, w, "GET", "/api/v1/host-apps", "")
	if !strings.Contains(rec.Body.String(), `"available":false`) {
		t.Fatalf("without Xpra: %s", rec.Body)
	}
}

// TestHostAppsConnect: Tilde's own Xpra client (D80) reaches the session
// through a WebSocket that the worker relays to its socket, bytes as they
// come, whatever the messages' boundaries; it never starts the session,
// and ends with it.
func TestHostAppsConnect(t *testing.T) {
	fakeXpra(t)
	w := driveWorker(t, nil)
	t.Cleanup(w.xpra.stop)
	srv := httptest.NewServer(w.Handler())
	t.Cleanup(srv.Close)
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/v1/host-apps/connect"
	ctx := t.Context()

	if _, res, err := websocket.Dial(ctx, url, nil); err == nil || res == nil || res.StatusCode != http.StatusNotFound {
		t.Fatalf("before the session: %v %v", res, err)
	}
	if rec := serve(t, w, "POST", "/api/v1/host-apps/session", ``); rec.Code != http.StatusNoContent {
		t.Fatalf("session: %d %s", rec.Code, rec.Body)
	}
	// A plain GET, which the server lets through without an Origin check,
	// does not reach Xpra.
	if rec := serve(t, w, "GET", "/api/v1/host-apps/connect", ``); rec.Code != http.StatusUpgradeRequired {
		t.Fatalf("plain GET: %d %s", rec.Code, rec.Body)
	}

	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	c.SetReadLimit(1 << 20) // the relay sends up to 256 KiB at a time
	sent := append([]byte("P\x10\x00\x00"), bytes.Repeat([]byte("pixels"), 100_000)...)
	for _, part := range [][]byte{sent[:3], sent[3:70_000], sent[70_000:]} {
		if err := c.Write(ctx, websocket.MessageBinary, part); err != nil {
			t.Fatal(err)
		}
	}
	var got []byte
	for len(got) < len(sent) {
		typ, b, err := c.Read(ctx)
		if err != nil || typ != websocket.MessageBinary {
			t.Fatalf("read after %d bytes: %v %v", len(got), typ, err)
		}
		got = append(got, b...)
	}
	if !bytes.Equal(got, sent) {
		t.Fatalf("relayed %d bytes, not the %d sent", len(got), len(sent))
	}

	// Text is not Xpra's.
	c.Write(ctx, websocket.MessageText, []byte("hello"))
	if _, _, err := c.Read(ctx); websocket.CloseStatus(err) != websocket.StatusUnsupportedData {
		t.Fatalf("after text: %v", err)
	}

	// The session ending ends the connection.
	c, _, err = websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	w.xpra.stop()
	if _, _, err := c.Read(ctx); websocket.CloseStatus(err) != websocket.StatusNormalClosure {
		t.Fatalf("after the session: %v", err)
	}
}
