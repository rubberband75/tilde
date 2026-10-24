package worker

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

type savedText struct {
	Version string
	Entry   Entry
}

func textGet(t *testing.T, c *http.Client, path string) *http.Response {
	t.Helper()
	resp, err := c.Get(api("/api/v1/text", url.Values{"path": {path}}))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func textPut(t *testing.T, c *http.Client, path, base, text string, force bool) *http.Response {
	t.Helper()
	q := url.Values{"path": {path}, "base": {base}}
	if force {
		q.Set("force", "1")
	}
	req, _ := http.NewRequest(http.MethodPut, api("/api/v1/text", q), strings.NewReader(text))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// TestTextReadAndSave: the editor reads UTF-8 text with a version, saves
// against it, and is told when the file changed on disk meanwhile (D48).
func TestTextReadAndSave(t *testing.T) {
	c, home := startWorker(t)
	p := filepath.Join(home, "notes.txt")
	write(t, p, "héllo\n")
	if err := os.Chmod(p, 0o640); err != nil {
		t.Fatal(err)
	}

	f := decode[textFile](t, textGet(t, c, p), http.StatusOK)
	if f.Text != "héllo\n" || !f.Writable || f.Version == "" || f.Entry.Name != "notes.txt" {
		t.Fatalf("read %+v", f)
	}
	s := decode[savedText](t, textPut(t, c, p, f.Version, "bye\n", false), http.StatusOK)
	if got, _ := os.ReadFile(p); string(got) != "bye\n" || s.Version == f.Version {
		t.Fatalf("saved %q, version %s", got, s.Version)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o640 {
		t.Fatalf("permissions became %v", st.Mode().Perm())
	}

	// Saving from the old version: the file changed since.
	decode[apiError](t, textPut(t, c, p, f.Version, "lost\n", false), http.StatusConflict)
	decode[savedText](t, textPut(t, c, p, f.Version, "forced\n", true), http.StatusOK)
	if got, _ := os.ReadFile(p); string(got) != "forced\n" {
		t.Fatalf("forced save: %q", got)
	}

	// Save As: a new file, and not over an existing one unless asked.
	q := filepath.Join(home, "new.txt")
	decode[savedText](t, textPut(t, c, q, "", "new\n", false), http.StatusOK)
	if e := decode[apiError](t, textPut(t, c, q, "", "again\n", false), http.StatusConflict); e.Code != "exists" {
		t.Fatalf("save as over a file: %+v", e)
	}
	os.Remove(q)
	if e := decode[apiError](t, textPut(t, c, q, s.Version, "x", false), http.StatusConflict); e.Code != "changed" {
		t.Fatalf("save to a deleted file: %+v", e)
	}
	entries, _ := os.ReadDir(home)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tilde-save-") {
			t.Fatalf("left a temporary file: %s", e.Name())
		}
	}
}

// TestTextNotText: binary files, other encodings, folders and big files
// are refused with a reason the editor can show.
func TestTextNotText(t *testing.T) {
	c, home := startWorker(t)
	for name, content := range map[string][]byte{
		"bin":    {'a', 0, 'b'},
		"latin1": {'c', 'a', 'f', 0xe9},
	} {
		p := filepath.Join(home, name)
		os.WriteFile(p, content, 0o644)
		if e := decode[apiError](t, textGet(t, c, p), http.StatusUnsupportedMediaType); e.Code != "not_text" {
			t.Fatalf("%s: %+v", name, e)
		}
	}
	big := filepath.Join(home, "big")
	os.WriteFile(big, bytes.Repeat([]byte("x"), maxTextSize+1), 0o644)
	if e := decode[apiError](t, textGet(t, c, big), http.StatusRequestEntityTooLarge); e.Code != "too_large" {
		t.Fatalf("big: %+v", e)
	}
	decode[apiError](t, textGet(t, c, home), http.StatusBadRequest)
	decode[apiError](t, textGet(t, c, filepath.Join(home, "missing")), http.StatusNotFound)
	decode[apiError](t, textPut(t, c, filepath.Join(home, "x"), "", "bad \xff", false), http.StatusBadRequest)
}

// TestTextKeepsTheFile: saving writes a symlink's target and keeps hard
// links, as GIO does: the link stays a link, both names stay one file.
func TestTextKeepsTheFile(t *testing.T) {
	c, home := startWorker(t)
	target := filepath.Join(home, "real.conf")
	link := filepath.Join(home, "link.conf")
	write(t, target, "a=1\n")
	os.Symlink("real.conf", link)
	f := decode[textFile](t, textGet(t, c, link), http.StatusOK)
	decode[savedText](t, textPut(t, c, link, f.Version, "a=2\n", false), http.StatusOK)
	if lst, _ := os.Lstat(link); lst.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the symlink was replaced by a file")
	}
	if got, _ := os.ReadFile(target); string(got) != "a=2\n" {
		t.Fatalf("target holds %q", got)
	}

	hard := filepath.Join(home, "hard.conf")
	os.Link(target, hard)
	before, _ := os.Stat(target)
	f = decode[textFile](t, textGet(t, c, hard), http.StatusOK)
	decode[savedText](t, textPut(t, c, hard, f.Version, "a=3\n", false), http.StatusOK)
	after, _ := os.Stat(target)
	if got, _ := os.ReadFile(target); string(got) != "a=3\n" ||
		before.Sys().(*syscall.Stat_t).Ino != after.Sys().(*syscall.Stat_t).Ino {
		t.Fatalf("hard link broken: %q", got)
	}
}

// TestDrafts: unsaved text is kept by key until it is deleted, readable
// only by the user (D48).
func TestDrafts(t *testing.T) {
	c, home := startWorker(t)
	key := "file:" + filepath.Join(home, "notes.txt")
	q := url.Values{"key": {key}}
	decode[apiError](t, mustGet(t, c, api("/api/v1/drafts", q)), http.StatusNotFound)

	b, _ := json.Marshal(map[string]string{"text": "unsaved\n", "base": "v1"})
	req, _ := http.NewRequest(http.MethodPut, api("/api/v1/drafts", q), bytes.NewReader(b))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	decode[any](t, resp, http.StatusNoContent)
	d := decode[Draft](t, mustGet(t, c, api("/api/v1/drafts", q)), http.StatusOK)
	if d.Key != key || d.Text != "unsaved\n" || d.Base != "v1" || d.Updated.IsZero() {
		t.Fatalf("draft %+v", d)
	}
	dir := filepath.Join(home, ".local", "state", "tilde", "drafts")
	if st, err := os.Stat(dir); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("drafts folder: %v %v", st, err)
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 1 {
		t.Fatalf("draft files %v", files)
	}
	if st, _ := os.Stat(filepath.Join(dir, files[0].Name())); st.Mode().Perm() != 0o600 {
		t.Fatalf("draft file mode %v", st.Mode().Perm())
	}

	req, _ = http.NewRequest(http.MethodDelete, api("/api/v1/drafts", q), nil)
	resp, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	decode[any](t, resp, http.StatusNoContent)
	decode[apiError](t, mustGet(t, c, api("/api/v1/drafts", q)), http.StatusNotFound)
	decode[apiError](t, mustGet(t, c, api("/api/v1/drafts", nil)), http.StatusBadRequest)
}

func mustGet(t *testing.T, c *http.Client, u string) *http.Response {
	t.Helper()
	resp, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}
