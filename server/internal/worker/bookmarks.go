package worker

import (
	"bufio"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Bookmarks live in ~/.config/gtk-3.0/bookmarks, the file Nemo and GTK's
// file chooser use, so they are shared. Each line is "URI [name]". Lines
// Tilde does not show (sftp://, smb://) are kept as they are.

// Bookmark is a sidebar shortcut to a local folder.
type Bookmark struct {
	Path string `json:"path"`
	Name string `json:"name"`
	// Exists is false for a bookmark whose folder is gone (or unmounted).
	Exists bool `json:"exists"`
}

type bookmarkLine struct {
	raw  string // for lines that are not local folders
	path string
	name string // explicit label, may be empty
}

var bookmarksMu sync.Mutex

func (w *Worker) bookmarksFile() string {
	return filepath.Join(w.configHome, "gtk-3.0", "bookmarks")
}

func (w *Worker) readBookmarks() ([]bookmarkLine, error) {
	f, err := os.Open(w.bookmarksFile())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines []bookmarkLine
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		uri, name, _ := strings.Cut(line, " ")
		if p, err := pathFromURI(uri); err == nil {
			lines = append(lines, bookmarkLine{path: p, name: name})
		} else {
			lines = append(lines, bookmarkLine{raw: line})
		}
	}
	return lines, sc.Err()
}

func (w *Worker) writeBookmarks(lines []bookmarkLine) error {
	var b strings.Builder
	for _, l := range lines {
		switch {
		case l.raw != "":
			b.WriteString(l.raw)
		case l.name != "":
			b.WriteString(fileURI(l.path) + " " + l.name)
		default:
			b.WriteString(fileURI(l.path))
		}
		b.WriteByte('\n')
	}
	return writeFileAtomic(w.bookmarksFile(), []byte(b.String()), 0o644)
}

func toBookmarks(lines []bookmarkLine) []Bookmark {
	out := []Bookmark{}
	for _, l := range lines {
		if l.raw != "" {
			continue
		}
		name := l.name
		if name == "" {
			name = filepath.Base(l.path)
		}
		st, err := os.Stat(l.path)
		out = append(out, Bookmark{Path: l.path, Name: name, Exists: err == nil && st.IsDir()})
	}
	return out
}

func (w *Worker) handleBookmarks(rw http.ResponseWriter, r *http.Request) {
	bookmarksMu.Lock()
	lines, err := w.readBookmarks()
	bookmarksMu.Unlock()
	if err != nil {
		writeErr(rw, err)
		return
	}
	writeJSON(rw, http.StatusOK, map[string]any{"bookmarks": toBookmarks(lines)})
}

// handleBookmarkAdd adds a bookmark, or renames it if the folder is already
// bookmarked. An empty name means the folder's own name.
func (w *Worker) handleBookmarkAdd(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(rw, err)
		return
	}
	p, err := cleanAbs(req.Path)
	if err != nil {
		writeErr(rw, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if strings.ContainsAny(name, "\n\r") {
		writeErr(rw, badRequest{"invalid name"})
		return
	}
	if name == filepath.Base(p) {
		name = ""
	}
	w.updateBookmarks(rw, func(lines []bookmarkLine) []bookmarkLine {
		for i := range lines {
			if lines[i].raw == "" && lines[i].path == p {
				lines[i].name = name
				return lines
			}
		}
		return append(lines, bookmarkLine{path: p, name: name})
	})
}

func (w *Worker) handleBookmarkRemove(rw http.ResponseWriter, r *http.Request) {
	p, err := queryPath(r, "path")
	if err != nil {
		writeErr(rw, err)
		return
	}
	w.updateBookmarks(rw, func(lines []bookmarkLine) []bookmarkLine {
		out := lines[:0]
		for _, l := range lines {
			if l.raw != "" || l.path != p {
				out = append(out, l)
			}
		}
		return out
	})
}

func (w *Worker) updateBookmarks(rw http.ResponseWriter, change func([]bookmarkLine) []bookmarkLine) {
	bookmarksMu.Lock()
	defer bookmarksMu.Unlock()
	lines, err := w.readBookmarks()
	if err != nil {
		writeErr(rw, err)
		return
	}
	lines = change(lines)
	if err := w.writeBookmarks(lines); err != nil {
		writeErr(rw, err)
		return
	}
	writeJSON(rw, http.StatusOK, map[string]any{"bookmarks": toBookmarks(lines)})
}
