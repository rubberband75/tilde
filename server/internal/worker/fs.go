package worker

import (
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Entry describes one file or directory.
type Entry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Type    string    `json:"type"` // "dir", "file" or "other"
	Symlink bool      `json:"symlink,omitempty"`
	Target  string    `json:"target,omitempty"`
	Broken  bool      `json:"broken,omitempty"`
	Size    int64     `json:"size"`
	Mode    string    `json:"mode"` // like ls: "-rw-r--r--"
	Perm    uint32    `json:"perm"` // octal permission bits, e.g. 0644
	UID     uint32    `json:"uid"`
	GID     uint32    `json:"gid"`
	MTime   time.Time `json:"mtime"`
	MIME    string    `json:"mime"`
	Hidden  bool      `json:"hidden,omitempty"`
}

// ListResponse is returned by GET /api/v1/fs/list.
type ListResponse struct {
	Path    string  `json:"path"`
	Parent  string  `json:"parent,omitempty"`
	Entries []Entry `json:"entries"`
}

func entryFor(path string, lst fs.FileInfo) Entry {
	name := lst.Name()
	if path == "/" {
		name = "/"
	}
	e := Entry{
		Name:   name,
		Path:   path,
		Hidden: strings.HasPrefix(name, ".") || strings.HasSuffix(name, "~"),
	}
	info := lst
	if lst.Mode()&fs.ModeSymlink != 0 {
		e.Symlink = true
		e.Target, _ = os.Readlink(path)
		if st, err := os.Stat(path); err == nil {
			info = st
		} else {
			e.Broken = true
		}
	}
	switch {
	case info.IsDir():
		e.Type = "dir"
	case info.Mode().IsRegular():
		e.Type = "file"
	default:
		e.Type = "other"
	}
	e.Size = info.Size()
	e.Mode = info.Mode().String()
	e.Perm = uint32(info.Mode().Perm())
	e.MTime = info.ModTime().UTC()
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		e.UID, e.GID = st.Uid, st.Gid
	}
	e.MIME = mimeFor(name, info, e.Broken)
	return e
}

// mimeFor guesses a MIME type from the file name. On Linux, Go's mime
// package reads the shared-mime-info globs (/usr/share/mime/globs2) first,
// so this mostly agrees with Nemo. Content sniffing (magic) is a TODO.
func mimeFor(name string, info fs.FileInfo, broken bool) string {
	switch {
	case broken:
		return "inode/symlink"
	case info.IsDir():
		return "inode/directory"
	case !info.Mode().IsRegular():
		return "inode/x-special"
	}
	if ext := filepath.Ext(name); ext != "" {
		if t := mime.TypeByExtension(strings.ToLower(ext)); t != "" {
			if i := strings.IndexByte(t, ';'); i >= 0 {
				t = t[:i]
			}
			return strings.TrimSpace(t)
		}
	}
	if info.Mode().Perm()&0o111 != 0 {
		return "application/x-executable"
	}
	return "application/octet-stream"
}

func listDir(dir string) (*ListResponse, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	des, err := f.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	resp := &ListResponse{Path: dir, Entries: make([]Entry, 0, len(des))}
	if dir != "/" {
		resp.Parent = filepath.Dir(dir)
	}
	for _, de := range des {
		info, err := de.Info()
		if err != nil {
			continue // vanished between readdir and lstat
		}
		resp.Entries = append(resp.Entries, entryFor(filepath.Join(dir, de.Name()), info))
	}
	sort.Slice(resp.Entries, func(i, j int) bool {
		a, b := resp.Entries[i], resp.Entries[j]
		if (a.Type == "dir") != (b.Type == "dir") {
			return a.Type == "dir"
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return resp, nil
}

func (w *Worker) handleList(rw http.ResponseWriter, r *http.Request) {
	dir, err := queryPath(r, "path")
	if err != nil {
		writeErr(rw, err)
		return
	}
	resp, err := listDir(dir)
	if err != nil {
		writeErr(rw, err)
		return
	}
	writeJSON(rw, http.StatusOK, resp)
}

func (w *Worker) handleStat(rw http.ResponseWriter, r *http.Request) {
	p, err := queryPath(r, "path")
	if err != nil {
		writeErr(rw, err)
		return
	}
	lst, err := os.Lstat(p)
	if err != nil {
		writeErr(rw, err)
		return
	}
	writeJSON(rw, http.StatusOK, entryFor(p, lst))
}

// inlineSafe lists types the browser may display directly. Everything is
// also served with a CSP sandbox (except PDF, whose viewer needs to run), so
// an HTML or SVG file from someone's home directory can never run script on
// Tilde's origin.
func inlineSafe(ctype string) bool {
	switch {
	case strings.HasPrefix(ctype, "image/"),
		strings.HasPrefix(ctype, "audio/"),
		strings.HasPrefix(ctype, "video/"),
		ctype == "text/plain",
		ctype == "application/pdf":
		return true
	}
	return false
}

// handleDownload streams a file. http.ServeContent provides Range requests,
// so the media player can seek.
func (w *Worker) handleDownload(rw http.ResponseWriter, r *http.Request) {
	p, err := queryPath(r, "path")
	if err != nil {
		writeErr(rw, err)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		writeErr(rw, err)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		writeErr(rw, err)
		return
	}
	if st.IsDir() {
		writeErr(rw, badRequest{"cannot download a directory"})
		return
	}
	name := filepath.Base(p)
	ctype := mimeFor(name, st, false)
	inline := r.URL.Query().Get("download") != "1" && inlineSafe(ctype)

	h := rw.Header()
	if ctype == "text/plain" {
		h.Set("Content-Type", "text/plain; charset=utf-8")
	} else {
		h.Set("Content-Type", ctype)
	}
	h.Set("X-Content-Type-Options", "nosniff")
	if ctype != "application/pdf" {
		h.Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self' data:; media-src 'self'; style-src 'unsafe-inline'")
	}
	disposition := "attachment"
	if inline {
		disposition = "inline"
	}
	h.Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": name}))
	h.Set("Cache-Control", "private, no-cache")
	http.ServeContent(rw, r, name, st.ModTime(), f)
}

// handleUpload writes the request body to path. The data goes to a temporary
// file in the same directory first, so a cancelled upload never leaves a
// half-written file under the real name.
func (w *Worker) handleUpload(rw http.ResponseWriter, r *http.Request) {
	dest, err := queryPath(r, "path")
	if err != nil {
		writeErr(rw, err)
		return
	}
	if !validName(filepath.Base(dest)) || dest == "/" {
		writeErr(rw, badRequest{"invalid file name"})
		return
	}
	overwrite := r.URL.Query().Get("overwrite") == "1"
	if !overwrite {
		if _, err := os.Lstat(dest); err == nil {
			writeErr(rw, fs.ErrExist)
			return
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".tilde-upload-*")
	if err != nil {
		writeErr(rw, err)
		return
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()
	if _, err := io.Copy(tmp, r.Body); err != nil {
		writeErr(rw, err)
		return
	}
	if err := tmp.Sync(); err != nil {
		writeErr(rw, err)
		return
	}
	if err := tmp.Close(); err != nil {
		writeErr(rw, err)
		return
	}
	// CreateTemp uses 0600; give the file normal permissions under the
	// user's umask.
	if err := os.Chmod(tmpName, 0o666&^w.umask); err != nil {
		writeErr(rw, err)
		return
	}
	if err := commitFile(tmpName, dest, overwrite); err != nil {
		writeErr(rw, err)
		return
	}
	committed = true
	lst, err := os.Lstat(dest)
	if err != nil {
		writeErr(rw, err)
		return
	}
	writeJSON(rw, http.StatusCreated, entryFor(dest, lst))
}

// commitFile moves tmp to dest. Without overwrite it uses link+unlink, which
// fails atomically if dest appeared meanwhile; filesystems without hard
// links (FAT, exFAT) fall back to a checked rename.
func commitFile(tmp, dest string, overwrite bool) error {
	if overwrite {
		return os.Rename(tmp, dest)
	}
	err := os.Link(tmp, dest)
	if err == nil {
		os.Remove(tmp)
		return nil
	}
	if errors.Is(err, fs.ErrExist) {
		return err
	}
	if _, statErr := os.Lstat(dest); statErr == nil {
		return fs.ErrExist
	}
	return os.Rename(tmp, dest)
}

func (w *Worker) handleMkdir(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
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
	if err := os.Mkdir(p, 0o777); err != nil {
		writeErr(rw, err)
		return
	}
	lst, err := os.Lstat(p)
	if err != nil {
		writeErr(rw, err)
		return
	}
	writeJSON(rw, http.StatusCreated, entryFor(p, lst))
}

// handleRename renames or moves within one filesystem. Moves across drives
// need a copy job (see docs/roadmap.md) and are reported as "other_drive".
func (w *Worker) handleRename(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		From      string `json:"from"`
		To        string `json:"to"`
		Overwrite bool   `json:"overwrite"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(rw, err)
		return
	}
	from, err := cleanAbs(req.From)
	if err != nil {
		writeErr(rw, err)
		return
	}
	to, err := cleanAbs(req.To)
	if err != nil {
		writeErr(rw, err)
		return
	}
	if !validName(filepath.Base(to)) {
		writeErr(rw, badRequest{"invalid file name"})
		return
	}
	if !req.Overwrite && from != to {
		if _, err := os.Lstat(to); err == nil {
			writeErr(rw, fs.ErrExist)
			return
		}
	}
	if err := os.Rename(from, to); err != nil {
		writeErr(rw, err)
		return
	}
	lst, err := os.Lstat(to)
	if err != nil {
		writeErr(rw, err)
		return
	}
	writeJSON(rw, http.StatusOK, entryFor(to, lst))
}
