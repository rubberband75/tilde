package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"

	"tilde/internal/mux"
	"tilde/internal/userinfo"
)

// startWorker runs a worker for the current user over an in-memory pipe and
// returns an HTTP client that reaches it through yamux, the same way the
// main server does.
func startWorker(t *testing.T) (*http.Client, string) {
	t.Helper()
	return startWorkerWith(t, nil)
}

// startWorkerWith is startWorker with a logind connection (D41).
func startWorkerWith(t *testing.T, logind *logindConn) (*http.Client, string) {
	t.Helper()
	info, err := userinfo.Current()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	info.Home = home
	info.Shell = "/bin/sh"
	w := New(info, Options{
		DataHome:        filepath.Join(home, ".local", "share"),
		ConfigHome:      filepath.Join(home, ".config"),
		CacheHome:       filepath.Join(home, ".cache"),
		ThumbnailerDirs: []string{filepath.Join(home, ".local", "share", "thumbnailers")},
	})
	if logind != nil {
		w.logind = logind
	}

	serverEnd, clientEnd := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = Serve(ctx, serverEnd, w.Handler())
	}()
	sess, err := yamux.Client(clientEnd, mux.Config())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		w.Shutdown()
		sess.Close()
		cancel()
		<-done
	})
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return sess.Open() },
		},
	}
	return client, home
}

func api(path string, q url.Values) string {
	u := "http://worker" + path
	if q != nil {
		u += "?" + q.Encode()
	}
	return u
}

func decode[T any](t *testing.T, resp *http.Response, wantStatus int) T {
	t.Helper()
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s: status %d, want %d: %s", resp.Request.Method, resp.Request.URL, resp.StatusCode, wantStatus, body)
	}
	var v T
	if len(body) > 0 {
		if err := json.Unmarshal(body, &v); err != nil {
			t.Fatalf("decode %s: %v", body, err)
		}
	}
	return v
}

func postJSON(t *testing.T, c *http.Client, path string, v any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(v)
	resp, err := c.Post(api(path, nil), "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestHello(t *testing.T) {
	c, home := startWorker(t)
	resp, err := c.Get(api("/api/v1/hello", nil))
	if err != nil {
		t.Fatal(err)
	}
	got := decode[map[string]any](t, resp, http.StatusOK)
	if got["home"] != home {
		t.Fatalf("home = %v, want %s", got["home"], home)
	}
}

func TestFileLifecycle(t *testing.T) {
	c, home := startWorker(t)

	// mkdir
	dir := filepath.Join(home, "Projects")
	decode[Entry](t, postJSON(t, c, "/api/v1/fs/mkdir", map[string]string{"path": dir}), http.StatusCreated)

	// upload
	file := filepath.Join(dir, "notes.txt")
	req, _ := http.NewRequest(http.MethodPut, api("/api/v1/fs/content", url.Values{"path": {file}}), strings.NewReader("hello tilde"))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	e := decode[Entry](t, resp, http.StatusCreated)
	if e.Size != 11 || e.MIME != "text/plain" {
		t.Fatalf("uploaded entry = %+v", e)
	}

	// upload again without overwrite fails
	req, _ = http.NewRequest(http.MethodPut, api("/api/v1/fs/content", url.Values{"path": {file}}), strings.NewReader("x"))
	resp, _ = c.Do(req)
	decode[apiError](t, resp, http.StatusConflict)

	// list
	resp, _ = c.Get(api("/api/v1/fs/list", url.Values{"path": {dir}}))
	list := decode[ListResponse](t, resp, http.StatusOK)
	if len(list.Entries) != 1 || list.Entries[0].Name != "notes.txt" {
		t.Fatalf("list = %+v", list)
	}

	// download with a range request
	req, _ = http.NewRequest(http.MethodGet, api("/api/v1/fs/content", url.Values{"path": {file}}), nil)
	req.Header.Set("Range", "bytes=6-")
	resp, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || string(body) != "tilde" {
		t.Fatalf("range download: %d %q", resp.StatusCode, body)
	}
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatal("downloads must carry a sandbox CSP")
	}

	// rename
	renamed := filepath.Join(dir, "todo.txt")
	decode[Entry](t, postJSON(t, c, "/api/v1/fs/rename", map[string]string{"from": file, "to": renamed}), http.StatusOK)

	// trash and restore
	res := decode[struct {
		Results []struct{ Name, Error string }
	}](t, postJSON(t, c, "/api/v1/fs/trash", map[string][]string{"paths": {renamed}}), http.StatusOK)
	if len(res.Results) != 1 || res.Results[0].Error != "" {
		t.Fatalf("trash results = %+v", res)
	}
	if _, err := os.Stat(renamed); !os.IsNotExist(err) {
		t.Fatal("trashed file still exists")
	}
	resp, _ = c.Get(api("/api/v1/trash", nil))
	trash := decode[struct{ Items []TrashItem }](t, resp, http.StatusOK)
	if len(trash.Items) != 1 || trash.Items[0].OriginalPath != renamed {
		t.Fatalf("trash = %+v", trash)
	}
	decode[map[string]any](t, postJSON(t, c, "/api/v1/trash/restore", map[string][]string{"names": {trash.Items[0].Name}}), http.StatusOK)
	if _, err := os.Stat(renamed); err != nil {
		t.Fatalf("restored file missing: %v", err)
	}

	// errors map to statuses
	resp, _ = c.Get(api("/api/v1/fs/list", url.Values{"path": {filepath.Join(home, "missing")}}))
	decode[apiError](t, resp, http.StatusNotFound)
	resp, _ = c.Get(api("/api/v1/fs/list", url.Values{"path": {"relative/path"}}))
	decode[apiError](t, resp, http.StatusBadRequest)
}

func TestTrashSpecFormat(t *testing.T) {
	data := t.TempDir()
	tr := NewTrash(data)
	src := filepath.Join(t.TempDir(), "a file.txt")
	os.WriteFile(src, []byte("x"), 0o644)
	name, err := tr.Put(src)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.ReadFile(filepath.Join(data, "Trash", "info", name+".trashinfo"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(info)
	if !strings.HasPrefix(s, "[Trash Info]\n") || !strings.Contains(s, "a%20file.txt") || !strings.Contains(s, "DeletionDate=") {
		t.Fatalf("trashinfo = %q", s)
	}
	// A second item with the same name gets a new trash name.
	os.WriteFile(src, []byte("y"), 0o644)
	name2, err := tr.Put(src)
	if err != nil || name2 == name {
		t.Fatalf("second put: %q, %v", name2, err)
	}
}

func TestTerminalAttachAndReplay(t *testing.T) {
	c, _ := startWorker(t)
	resp := postJSON(t, c, "/api/v1/terminals", map[string]int{"cols": 80, "rows": 24})
	term := decode[TerminalInfo](t, resp, http.StatusCreated)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	attach := func() *websocket.Conn {
		conn, _, err := websocket.Dial(ctx, "ws://worker/api/v1/terminals/"+term.ID+"/attach", &websocket.DialOptions{HTTPClient: c})
		if err != nil {
			t.Fatal(err)
		}
		return conn
	}
	readUntil := func(conn *websocket.Conn, want string) {
		var got strings.Builder
		for !strings.Contains(got.String(), want) {
			_, data, err := conn.Read(ctx)
			if err != nil {
				t.Fatalf("waiting for %q, got %q: %v", want, got.String(), err)
			}
			got.Write(data)
		}
	}

	conn := attach()
	conn.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","cols":100,"rows":30}`))
	conn.Write(ctx, websocket.MessageBinary, []byte("echo tilde-$((40+2))\n"))
	readUntil(conn, "tilde-42")
	conn.Close(websocket.StatusNormalClosure, "")

	// A new client gets the earlier output replayed.
	conn = attach()
	readUntil(conn, "tilde-42")
	conn.Close(websocket.StatusNormalClosure, "")

	resp, _ = c.Get(api("/api/v1/terminals", nil))
	list := decode[struct{ Terminals []TerminalInfo }](t, resp, http.StatusOK)
	if len(list.Terminals) != 1 || list.Terminals[0].Cols != 100 {
		t.Fatalf("terminals = %+v", list)
	}

	req, _ := http.NewRequest(http.MethodDelete, api("/api/v1/terminals/"+term.ID, nil), nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status %d", resp.StatusCode)
	}
	// The terminal disappears from the list once the shell has exited.
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, _ = c.Get(api("/api/v1/terminals", nil))
		list = decode[struct{ Terminals []TerminalInfo }](t, resp, http.StatusOK)
		if len(list.Terminals) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(fmt.Sprintf("terminal still listed: %+v", list))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestUserDirs(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, ".config")
	os.MkdirAll(cfg, 0o755)
	os.WriteFile(filepath.Join(cfg, "user-dirs.dirs"), []byte("# comment\nXDG_DOCUMENTS_DIR=\"$HOME/Dokumente\"\nXDG_DESKTOP_DIR=\"$HOME/Schreibtisch\"\n"), 0o644)
	d := userDirs(home, cfg)
	if d["DOCUMENTS"] != filepath.Join(home, "Dokumente") || d["DESKTOP"] != filepath.Join(home, "Schreibtisch") {
		t.Fatalf("userDirs = %v", d)
	}
}

func TestUnescapeMount(t *testing.T) {
	if got := unescapeMount(`/media/me/USB\040Drive`); got != "/media/me/USB Drive" {
		t.Fatalf("got %q", got)
	}
}
