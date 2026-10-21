package worker

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

// TestFileURI checks against what GLib.filename_to_uri returns on Mint 22.
func TestFileURI(t *testing.T) {
	for path, want := range map[string]string{
		"/home/me/My Files":      "file:///home/me/My%20Files",
		"/a/b-c_d.e~f/(x)!$&'*+": "file:///a/b-c_d.e~f/(x)!$&'*+",
		"/x/#?%;[]{}":            "file:///x/%23%3F%25%3B%5B%5D%7B%7D",
		"/ü":                     "file:///%C3%BC",
		"/a:b=c@d,e;f":           "file:///a:b=c@d,e%3Bf",
		"/\"<>\\^`|":             "file:///%22%3C%3E%5C%5E%60%7C",
	} {
		if got := fileURI(path); got != want {
			t.Errorf("fileURI(%q) = %q, want %q", path, got, want)
		}
		if back, err := pathFromURI(want); err != nil || back != path {
			t.Errorf("pathFromURI(%q) = %q, %v", want, back, err)
		}
	}
}

func TestBookmarks(t *testing.T) {
	c, home := startWorker(t)
	docs := filepath.Join(home, "My Docs")
	os.Mkdir(docs, 0o755)
	file := filepath.Join(home, ".config", "gtk-3.0", "bookmarks")
	write(t, file, "sftp://server/srv Server\nfile:///gone\n")

	type list struct{ Bookmarks []Bookmark }
	l := decode[list](t, postJSON(t, c, "/api/v1/bookmarks", map[string]string{"path": docs}), http.StatusOK)
	if len(l.Bookmarks) != 2 || l.Bookmarks[1] != (Bookmark{Path: docs, Name: "My Docs", Exists: true}) || l.Bookmarks[0].Exists {
		t.Fatalf("after add: %+v", l.Bookmarks)
	}
	// Adding again renames; the sftp line is kept untouched.
	decode[list](t, postJSON(t, c, "/api/v1/bookmarks", map[string]string{"path": docs, "name": "Papers"}), http.StatusOK)
	if got := read(t, file); got != "sftp://server/srv Server\nfile:///gone\nfile://"+fileURI(docs)[7:]+" Papers\n" {
		t.Fatalf("file = %q", got)
	}
	req, _ := http.NewRequest(http.MethodDelete, api("/api/v1/bookmarks", url.Values{"path": {"/gone"}}), nil)
	resp, _ := c.Do(req)
	l = decode[list](t, resp, http.StatusOK)
	if len(l.Bookmarks) != 1 || l.Bookmarks[0].Name != "Papers" {
		t.Fatalf("after remove: %+v", l.Bookmarks)
	}
	decode[apiError](t, postJSON(t, c, "/api/v1/bookmarks", map[string]string{"path": docs, "name": "a\nb"}), http.StatusBadRequest)
}
