package worker

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Trash implements the home trash of the freedesktop.org Trash spec
// (https://specifications.freedesktop.org/trash-spec/), so items trashed in
// Tilde show up in the real Nemo trash and the other way round.
//
// Not implemented yet: per-drive trash directories ($topdir/.Trash-$uid) for
// files on other filesystems, and the optional directorysizes cache.
type Trash struct {
	dir string // usually ~/.local/share/Trash
}

// TrashItem is one entry in the trash.
type TrashItem struct {
	Name         string    `json:"name"` // name inside Trash/files
	OriginalPath string    `json:"originalPath"`
	DeletedAt    time.Time `json:"deletedAt"`
	Entry        Entry     `json:"entry"`
}

// NewTrash returns the home trash under dataHome ($XDG_DATA_HOME).
func NewTrash(dataHome string) *Trash {
	return &Trash{dir: filepath.Join(dataHome, "Trash")}
}

// Dir returns the trash directory.
func (t *Trash) Dir() string { return t.dir }

const trashDateLayout = "2006-01-02T15:04:05"

func (t *Trash) ensure() error {
	for _, d := range []string{"files", "info"} {
		if err := os.MkdirAll(filepath.Join(t.dir, d), 0o700); err != nil {
			return err
		}
	}
	return nil
}

// Put moves path into the trash and returns its name inside Trash/files.
func (t *Trash) Put(path string) (string, error) {
	path = filepath.Clean(path)
	if path == "/" || strings.HasPrefix(path+"/", t.dir+"/") {
		return "", badRequest{"cannot move this item to the trash"}
	}
	if _, err := os.Lstat(path); err != nil {
		return "", err
	}
	if err := t.ensure(); err != nil {
		return "", err
	}
	base := filepath.Base(path)
	for i := 1; i < 10000; i++ {
		name := base
		if i > 1 {
			name = fmt.Sprintf("%s.%d", base, i)
		}
		infoPath := filepath.Join(t.dir, "info", name+".trashinfo")
		// Creating the .trashinfo with O_EXCL reserves the name atomically,
		// as the spec requires.
		f, err := os.OpenFile(infoPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, werr := fmt.Fprintf(f, "[Trash Info]\nPath=%s\nDeletionDate=%s\n",
			(&url.URL{Path: path}).EscapedPath(), time.Now().Format(trashDateLayout))
		cerr := f.Close()
		if werr != nil || cerr != nil {
			os.Remove(infoPath)
			return "", errors.Join(werr, cerr)
		}
		if err := os.Rename(path, filepath.Join(t.dir, "files", name)); err != nil {
			os.Remove(infoPath)
			if errors.Is(err, syscall.EXDEV) {
				return "", ErrOtherDrive
			}
			return "", err
		}
		return name, nil
	}
	return "", errors.New("too many items with the same name in the trash")
}

// List returns the items in the trash, newest first.
func (t *Trash) List() ([]TrashItem, error) {
	infos, err := os.ReadDir(filepath.Join(t.dir, "info"))
	if errors.Is(err, fs.ErrNotExist) {
		return []TrashItem{}, nil
	}
	if err != nil {
		return nil, err
	}
	items := make([]TrashItem, 0, len(infos))
	for _, de := range infos {
		name, ok := strings.CutSuffix(de.Name(), ".trashinfo")
		if !ok || !validName(name) {
			continue
		}
		orig, deleted, err := t.readInfo(name)
		if err != nil {
			continue
		}
		filePath := filepath.Join(t.dir, "files", name)
		lst, err := os.Lstat(filePath)
		if err != nil {
			continue // orphaned .trashinfo
		}
		items = append(items, TrashItem{
			Name:         name,
			OriginalPath: orig,
			DeletedAt:    deleted,
			Entry:        entryFor(filePath, lst),
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].DeletedAt.After(items[j].DeletedAt) })
	return items, nil
}

func (t *Trash) readInfo(name string) (string, time.Time, error) {
	f, err := os.Open(filepath.Join(t.dir, "info", name+".trashinfo"))
	if err != nil {
		return "", time.Time{}, err
	}
	defer f.Close()
	var orig string
	var deleted time.Time
	inSection := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			inSection = line == "[Trash Info]"
			continue
		}
		if !inSection {
			continue
		}
		if v, ok := strings.CutPrefix(line, "Path="); ok {
			if p, err := url.PathUnescape(v); err == nil {
				orig = p
			}
		} else if v, ok := strings.CutPrefix(line, "DeletionDate="); ok {
			if d, err := time.ParseInLocation(trashDateLayout, v, time.Local); err == nil {
				deleted = d
			}
		}
	}
	if orig == "" {
		return "", time.Time{}, errors.New("trashinfo without Path")
	}
	if !filepath.IsAbs(orig) {
		// Relative paths are relative to the trash's top directory; for the
		// home trash that is the directory holding the trash.
		orig = filepath.Join(filepath.Dir(t.dir), orig)
	}
	return orig, deleted, nil
}

// Restore moves an item back to where it was deleted from.
func (t *Trash) Restore(name string) (string, error) {
	if !validName(name) {
		return "", badRequest{"invalid trash item name"}
	}
	orig, _, err := t.readInfo(name)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(orig); err == nil {
		return "", fs.ErrExist
	}
	if err := os.MkdirAll(filepath.Dir(orig), 0o777); err != nil {
		return "", err
	}
	if err := os.Rename(filepath.Join(t.dir, "files", name), orig); err != nil {
		return "", err
	}
	os.Remove(filepath.Join(t.dir, "info", name+".trashinfo"))
	return orig, nil
}

// Empty permanently deletes everything in the trash.
func (t *Trash) Empty() error {
	var errs []error
	for _, d := range []string{"files", "info"} {
		dir := filepath.Join(t.dir, d)
		des, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, de := range des {
			if err := os.RemoveAll(filepath.Join(dir, de.Name())); err != nil {
				errs = append(errs, err)
			}
		}
	}
	os.Remove(filepath.Join(t.dir, "directorysizes"))
	return errors.Join(errs...)
}

func (w *Worker) handleTrashPut(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		Paths []string `json:"paths"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(rw, err)
		return
	}
	type result struct {
		Path  string `json:"path"`
		Name  string `json:"name,omitempty"`
		Error string `json:"error,omitempty"`
		Code  string `json:"code,omitempty"`
	}
	results := make([]result, 0, len(req.Paths))
	for _, p := range req.Paths {
		clean, err := cleanAbs(p)
		if err == nil {
			var name string
			name, err = w.trash.Put(clean)
			if err == nil {
				results = append(results, result{Path: clean, Name: name})
				continue
			}
		}
		code := "error"
		if errors.Is(err, ErrOtherDrive) {
			code = "other_drive"
		} else if errors.Is(err, fs.ErrPermission) {
			code = "permission_denied"
		} else if errors.Is(err, fs.ErrNotExist) {
			code = "not_found"
		}
		results = append(results, result{Path: p, Error: err.Error(), Code: code})
	}
	writeJSON(rw, http.StatusOK, map[string]any{"results": results})
}

func (w *Worker) handleTrashList(rw http.ResponseWriter, r *http.Request) {
	items, err := w.trash.List()
	if err != nil {
		writeErr(rw, err)
		return
	}
	writeJSON(rw, http.StatusOK, map[string]any{"dir": w.trash.Dir(), "items": items})
}

func (w *Worker) handleTrashRestore(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		Names []string `json:"names"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(rw, err)
		return
	}
	restored := []string{}
	for _, n := range req.Names {
		p, err := w.trash.Restore(n)
		if err != nil {
			writeErr(rw, err)
			return
		}
		restored = append(restored, p)
	}
	writeJSON(rw, http.StatusOK, map[string]any{"restored": restored})
}

func (w *Worker) handleTrashEmpty(rw http.ResponseWriter, r *http.Request) {
	if err := w.trash.Empty(); err != nil {
		writeErr(rw, err)
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}
