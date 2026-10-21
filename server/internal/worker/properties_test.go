package worker

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"testing"
)

func TestProperties(t *testing.T) {
	c, home := startWorker(t)
	f := filepath.Join(home, "f.txt")
	write(t, f, "hello")
	resp, _ := c.Get(api("/api/v1/fs/properties", url.Values{"path": {f}}))
	p := decode[Properties](t, resp, http.StatusOK)
	me, _ := user.Current()
	if p.Owner != me.Username || !p.Mine || p.Capacity == 0 || p.Entry.Size != 5 || p.ATime.IsZero() {
		t.Fatalf("properties = %+v", p)
	}
}

func TestChmod(t *testing.T) {
	c, home := startWorker(t)
	f := filepath.Join(home, "script.sh")
	write(t, f, "#!/bin/sh\n")
	os.Chmod(f, 0o644|os.ModeSetgid)
	e := decode[Entry](t, postJSON(t, c, "/api/v1/fs/chmod", map[string]any{"path": f, "perm": 0o750}), http.StatusOK)
	st, _ := os.Stat(f)
	if e.Perm != 0o750 || st.Mode().Perm() != 0o750 || st.Mode()&os.ModeSetgid == 0 {
		t.Fatalf("perm %o, mode %v (setgid must be kept)", e.Perm, st.Mode())
	}
	decode[apiError](t, postJSON(t, c, "/api/v1/fs/chmod", map[string]any{"path": f, "perm": 0o4755}), http.StatusBadRequest)
	os.Symlink(f, filepath.Join(home, "link"))
	decode[apiError](t, postJSON(t, c, "/api/v1/fs/chmod", map[string]any{"path": filepath.Join(home, "link"), "perm": 0o777}), http.StatusBadRequest)
	if os.Geteuid() != 0 {
		decode[apiError](t, postJSON(t, c, "/api/v1/fs/chmod", map[string]any{"path": "/etc/passwd", "perm": 0o666}), http.StatusForbidden)
	}
}

func TestUsage(t *testing.T) {
	c, home := startWorker(t)
	write(t, filepath.Join(home, "a", "one.txt"), "12345")
	write(t, filepath.Join(home, "a", "sub", "two.txt"), "123")
	write(t, filepath.Join(home, "b.txt"), "1234567")
	os.Symlink("/etc", filepath.Join(home, "a", "link"))
	resp, err := c.Get(api("/api/v1/fs/usage", url.Values{"path": {filepath.Join(home, "a"), filepath.Join(home, "b.txt")}}))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var last struct {
		Items, Size, Unreadable int64
		Done                    bool
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		json.Unmarshal(sc.Bytes(), &last)
	}
	// a/ holds one.txt, sub, sub/two.txt and the link; b.txt adds its size.
	if !last.Done || last.Items != 4 || last.Size != 15 || last.Unreadable != 0 {
		t.Fatalf("usage = %+v", last)
	}
}
