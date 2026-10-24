package worker

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"unicode/utf8"
)

// Text files for the text editor (D48). Reading checks the file is text
// (UTF-8, no NUL bytes) and small enough to edit in a browser; saving
// keeps what the file is, as GIO's g_file_replace does for xed and gedit:
// a symlink's target is written, not replaced by a file, and a new copy
// is renamed into place only when that loses nothing (permissions, owner,
// hard links, ACLs); otherwise the file is rewritten in place. Each read
// returns a version, and a save names the version it started from, so a
// file changed on disk since is not overwritten without asking.

// maxTextSize bounds a file opened in the editor.
const maxTextSize = 10 << 20

// errNotText marks a file the editor cannot show: binary, or not UTF-8.
var errNotText = errors.New("this file is not UTF-8 text")

// fileVersion names a file's contents as of st: a save that names
// another version finds the file changed on disk.
func fileVersion(st fs.FileInfo) string {
	v := fmt.Sprintf("%d-%d", st.ModTime().UnixNano(), st.Size())
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		v += fmt.Sprintf("-%d", sys.Ino)
	}
	return v
}

// isText reports whether b can be edited as text.
func isText(b []byte) bool {
	return bytes.IndexByte(b, 0) < 0 && utf8.Valid(b)
}

type textFile struct {
	Text    string `json:"text"`
	Version string `json:"version"`
	// Writable: the user may save it (the kernel's answer, not a promise).
	Writable bool  `json:"writable"`
	Entry    Entry `json:"entry"`
}

func (w *Worker) handleTextGet(rw http.ResponseWriter, r *http.Request) {
	path, err := queryPath(r, "path")
	if err != nil {
		writeErr(rw, err)
		return
	}
	f, err := os.Open(path)
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
		writeErr(rw, syscall.EISDIR)
		return
	}
	if !st.Mode().IsRegular() {
		writeErr(rw, badRequest{"not a regular file"})
		return
	}
	if st.Size() > maxTextSize {
		writeJSON(rw, http.StatusRequestEntityTooLarge, apiError{Error: "the file is too large to edit here (over 10 MB)", Code: "too_large"})
		return
	}
	data, err := io.ReadAll(io.LimitReader(f, maxTextSize+1))
	if err != nil {
		writeErr(rw, err)
		return
	}
	if len(data) > maxTextSize {
		writeJSON(rw, http.StatusRequestEntityTooLarge, apiError{Error: "the file is too large to edit here (over 10 MB)", Code: "too_large"})
		return
	}
	if !isText(data) {
		writeJSON(rw, http.StatusUnsupportedMediaType, apiError{Error: errNotText.Error(), Code: "not_text"})
		return
	}
	lst, err := os.Lstat(path)
	if err != nil {
		writeErr(rw, err)
		return
	}
	writeJSON(rw, http.StatusOK, textFile{
		Text:     string(data),
		Version:  fileVersion(st),
		Writable: syscall.Access(path, 2 /* W_OK */) == nil,
		Entry:    entryFor(path, lst),
	})
}

// handleTextPut saves the body as path. base is the version the editor
// started from ("" for a new file); force saves over whatever is there.
func (w *Worker) handleTextPut(rw http.ResponseWriter, r *http.Request) {
	path, err := queryPath(r, "path")
	if err != nil {
		writeErr(rw, err)
		return
	}
	if !validName(filepath.Base(path)) || path == "/" {
		writeErr(rw, badRequest{"invalid file name"})
		return
	}
	q := r.URL.Query()
	base, force := q.Get("base"), q.Get("force") == "1"
	data, err := io.ReadAll(io.LimitReader(r.Body, maxTextSize+1))
	if err != nil {
		writeErr(rw, err)
		return
	}
	if len(data) > maxTextSize {
		writeJSON(rw, http.StatusRequestEntityTooLarge, apiError{Error: "the text is too large (over 10 MB)", Code: "too_large"})
		return
	}
	if !utf8.Valid(data) {
		writeErr(rw, badRequest{"the text is not UTF-8"})
		return
	}

	// Write through symlinks, as editors do: the link stays a link.
	target, err := filepath.EvalSymlinks(path)
	if errors.Is(err, fs.ErrNotExist) {
		target = path
	} else if err != nil {
		writeErr(rw, err)
		return
	}
	st, err := os.Stat(target)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if base != "" && !force {
			writeJSON(rw, http.StatusConflict, apiError{Error: "the file was deleted or moved since it was opened", Code: "changed"})
			return
		}
		err = w.createText(target, data)
	case err != nil:
	case !st.Mode().IsRegular():
		err = badRequest{"not a regular file"}
	case !force && base == "":
		err = fs.ErrExist
	case !force && fileVersion(st) != base:
		writeJSON(rw, http.StatusConflict, apiError{Error: "the file changed on disk since it was opened", Code: "changed"})
		return
	default:
		err = replaceText(target, st, data)
	}
	if err != nil {
		writeErr(rw, err)
		return
	}
	st, err = os.Stat(target)
	if err != nil {
		writeErr(rw, err)
		return
	}
	lst, err := os.Lstat(path)
	if err != nil {
		writeErr(rw, err)
		return
	}
	writeJSON(rw, http.StatusOK, map[string]any{"version": fileVersion(st), "entry": entryFor(path, lst)})
}

// createText makes a new file at path; it fails if one appeared meanwhile.
func (w *Worker) createText(path string, data []byte) error {
	tmp, err := writeTemp(path, data)
	if err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o666&^w.umask); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := commitFile(tmp, path, false); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// replaceText saves data over the regular file target. A new copy renamed
// into place never leaves a half-written file, but it is a new inode: it
// would drop hard links, ACLs and another user's ownership, so for those
// files, and when the folder is not writable, the file is rewritten in
// place instead.
func replaceText(target string, st fs.FileInfo, data []byte) error {
	sys, _ := st.Sys().(*syscall.Stat_t)
	inPlace := sys == nil || sys.Nlink > 1 || int(sys.Uid) != os.Getuid() || hasACL(target)
	if !inPlace {
		tmp, err := writeTemp(target, data)
		switch {
		case err == nil:
			// Same permissions and group; a group the user is not in
			// cannot be kept, and then neither can the inode.
			err = os.Chmod(tmp, st.Mode().Perm())
			if err == nil {
				err = os.Chown(tmp, -1, int(sys.Gid))
			}
			if err == nil {
				err = os.Rename(tmp, target)
			}
			if err == nil {
				return nil
			}
			os.Remove(tmp)
		case !errors.Is(err, fs.ErrPermission):
			return err
		}
	}
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// writeTemp writes data to a new hidden file next to path and returns its
// name.
func writeTemp(path string, data []byte) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tilde-save-*")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// hasACL reports whether path has a POSIX access ACL, which a new copy
// would not carry.
func hasACL(path string) bool {
	n, err := syscall.Getxattr(path, "system.posix_acl_access", nil)
	return err == nil && n > 0
}
