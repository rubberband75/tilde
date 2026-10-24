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
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"tilde/internal/protocol"
)

// Trash implements the freedesktop.org Trash spec
// (https://specifications.freedesktop.org/trash-spec/), so items trashed in
// Tilde show up in the real Nemo trash and the other way round.
//
// Files on the home filesystem go to the home trash. Files on other
// filesystems (USB drives, other partitions) go to that filesystem's trash,
// as GIO does: $topdir/.Trash/$uid when the administrator made a sticky
// $topdir/.Trash, else $topdir/.Trash-$uid. Moving them home would copy
// across drives and fill the home disk. Not implemented: the optional
// directorysizes cache.
type Trash struct {
	dir string // the home trash, usually ~/.local/share/Trash
	uid string

	// devOf and mountPoints are the filesystem as seen by the trash; tests
	// replace them to fake other drives.
	devOf       func(path string) (uint64, error)
	mountPoints func() []string
}

// trashDir is one trash: the home trash, or a drive's.
type trashDir struct {
	dir string // holds files/ and info/
	// top is the drive's $topdir; info files there hold paths relative to
	// it, so the drive can be mounted elsewhere. Empty for the home trash.
	top string
}

// TrashItem is one entry in the trash.
type TrashItem struct {
	// Name identifies the item: its name inside Trash/files for the home
	// trash, the absolute path inside a drive trash's files/ otherwise.
	Name         string    `json:"name"`
	OriginalPath string    `json:"originalPath"`
	DeletedAt    time.Time `json:"deletedAt"`
	Entry        Entry     `json:"entry"`
}

// NewTrash returns the trash with its home trash under dataHome
// ($XDG_DATA_HOME).
func NewTrash(dataHome string) *Trash {
	return &Trash{
		dir:   filepath.Join(dataHome, "Trash"),
		uid:   strconv.Itoa(os.Getuid()),
		devOf: deviceOf,
		mountPoints: func() []string {
			var out []string
			for _, m := range mounts() {
				out = append(out, m.MountPoint)
			}
			return out
		},
	}
}

func deviceOf(path string) (uint64, error) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return 0, &fs.PathError{Op: "stat", Path: path, Err: err}
	}
	return uint64(st.Dev), nil
}

// Dir returns the home trash directory.
func (t *Trash) Dir() string { return t.dir }

func (t *Trash) home() trashDir { return trashDir{dir: t.dir} }

const trashDateLayout = "2006-01-02T15:04:05"

func (td trashDir) ensure() error {
	for _, d := range []string{"files", "info"} {
		if err := os.MkdirAll(filepath.Join(td.dir, d), 0o700); err != nil {
			return err
		}
	}
	return nil
}

// name is how items of this trash are named in the API.
func (td trashDir) name(base string) string {
	if td.top == "" {
		return base
	}
	return filepath.Join(td.dir, "files", base)
}

// homeDev is the device of the home trash, or of its nearest existing
// parent while it does not exist yet.
func (t *Trash) homeDev() (uint64, error) {
	for p := t.dir; ; p = filepath.Dir(p) {
		dev, err := t.devOf(p)
		if err == nil || p == "/" {
			return dev, err
		}
	}
}

// topDir is the top of the filesystem holding dir: the last parent on the
// same device.
func (t *Trash) topDir(dir string, dev uint64) string {
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		if d, err := t.devOf(parent); err != nil || d != dev {
			return dir
		}
		dir = parent
	}
}

// forPath returns the trash an item at path goes to, creating a drive's
// trash if needed. ErrOtherDrive means the drive has no usable trash; the
// item can still be deleted.
func (t *Trash) forPath(path string) (trashDir, error) {
	parent := filepath.Dir(path)
	dev, err := t.devOf(parent)
	if err != nil {
		return trashDir{}, err
	}
	home, err := t.homeDev()
	if err != nil {
		return trashDir{}, err
	}
	if dev == home {
		return t.home(), nil
	}
	// Only drives get a trash: not tmpfs, /proc or other system mounts,
	// where GIO does not trash either.
	top := t.topDir(parent, dev)
	if !slices.Contains(t.mountPoints(), top) {
		return trashDir{}, ErrOtherDrive
	}
	if td, ok := t.driveTrash(top, true); ok {
		return td, nil
	}
	return trashDir{}, ErrOtherDrive
}

// driveTrash finds (and with create, makes) this user's trash on the drive
// at top, checking what the spec requires so another user cannot plant a
// trash (a symlink, a folder they own) to capture our files.
func (t *Trash) driveTrash(top string, create bool) (trashDir, bool) {
	// $topdir/.Trash, made by the administrator: a real folder with the
	// sticky bit. Then our folder in it.
	if adminTrash(top) {
		if td, ok := t.ownTrash(filepath.Join(top, ".Trash", t.uid), top, create); ok {
			return td, true
		}
	}
	return t.ownTrash(filepath.Join(top, ".Trash-"+t.uid), top, create)
}

// adminTrash reports whether top has a $topdir/.Trash the spec lets us use.
func adminTrash(top string) bool {
	st, err := os.Lstat(filepath.Join(top, ".Trash"))
	return err == nil && st.IsDir() && st.Mode()&os.ModeSticky != 0
}

// ownTrash accepts dir as a trash if it is a real folder of ours, creating
// it (0700) with create.
func (t *Trash) ownTrash(dir, top string, create bool) (trashDir, bool) {
	st, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) && create {
		if os.Mkdir(dir, 0o700) != nil {
			return trashDir{}, false
		}
		st, err = os.Lstat(dir)
	}
	if err != nil || !st.IsDir() {
		return trashDir{}, false
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); !ok || strconv.FormatUint(uint64(sys.Uid), 10) != t.uid {
		return trashDir{}, false
	}
	return trashDir{dir: dir, top: top}, true
}

// all returns the home trash and every existing trash of this user on the
// mounted drives.
func (t *Trash) all() []trashDir {
	out := []trashDir{t.home()}
	seen := map[string]bool{t.dir: true}
	home, _ := t.homeDev()
	for _, mp := range t.mountPoints() {
		if dev, err := t.devOf(mp); err != nil || dev == home {
			continue
		}
		// Both kinds can exist (the admin trash appeared later); list both.
		for _, dir := range []string{filepath.Join(mp, ".Trash", t.uid), filepath.Join(mp, ".Trash-"+t.uid)} {
			if td, ok := t.ofDir(dir); ok && !seen[td.dir] {
				seen[td.dir] = true
				out = append(out, td)
			}
		}
	}
	return out
}

// FilesDirs returns the files/ folder of every trash, for watching.
func (t *Trash) FilesDirs() []string {
	var out []string
	for _, td := range t.all() {
		out = append(out, filepath.Join(td.dir, "files"))
	}
	return out
}

// locate finds the trash and file name an item name refers to.
func (t *Trash) locate(name string) (trashDir, string, error) {
	if !filepath.IsAbs(name) {
		if !validName(name) {
			return trashDir{}, "", badRequest{"invalid trash item name"}
		}
		return t.home(), name, nil
	}
	name = filepath.Clean(name)
	files := filepath.Dir(name)
	if td, ok := t.ofDir(filepath.Dir(files)); ok && filepath.Base(files) == "files" && validName(filepath.Base(name)) {
		return td, filepath.Base(name), nil
	}
	return trashDir{}, "", badRequest{"invalid trash item name"}
}

// ofDir recognizes dir as one of this user's trashes.
func (t *Trash) ofDir(dir string) (trashDir, bool) {
	if dir == t.dir {
		return t.home(), true
	}
	switch base := filepath.Base(dir); {
	case base == ".Trash-"+t.uid:
		return t.ownTrash(dir, filepath.Dir(dir), false)
	case base == t.uid && filepath.Base(filepath.Dir(dir)) == ".Trash" && adminTrash(filepath.Dir(filepath.Dir(dir))):
		return t.ownTrash(dir, filepath.Dir(filepath.Dir(dir)), false)
	}
	return trashDir{}, false
}

// InfoFileFor returns the .trashinfo that belongs to path if path is an
// item in one of the user's trashes, so deleting it can remove both.
func (t *Trash) InfoFileFor(path string) (string, bool) {
	files := filepath.Dir(path)
	if filepath.Base(files) != "files" {
		return "", false
	}
	td, ok := t.ofDir(filepath.Dir(files))
	if !ok {
		return "", false
	}
	return filepath.Join(td.dir, "info", filepath.Base(path)+".trashinfo"), true
}

// inTrash reports whether path is a trash or inside one.
func (t *Trash) inTrash(path string) bool {
	if strings.HasPrefix(path+"/", t.dir+"/") {
		return true
	}
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if p == ".Trash-"+t.uid || p == ".Trash" && (i+1 == len(parts) || parts[i+1] == t.uid) {
			return true
		}
	}
	return false
}

// Put moves path into its trash and returns the item's name.
func (t *Trash) Put(path string) (string, error) {
	path = filepath.Clean(path)
	if path == "/" || t.inTrash(path) {
		return "", badRequest{"cannot move this item to the trash"}
	}
	if _, err := os.Lstat(path); err != nil {
		return "", err
	}
	td, err := t.forPath(path)
	if err != nil {
		return "", err
	}
	if err := td.ensure(); err != nil {
		return "", err
	}
	stored := path
	if td.top != "" {
		if rel, err := filepath.Rel(td.top, path); err == nil && !strings.HasPrefix(rel, "..") {
			stored = rel
		}
	}
	base := filepath.Base(path)
	for i := 1; i < 10000; i++ {
		name := base
		if i > 1 {
			name = fmt.Sprintf("%s.%d", base, i)
		}
		infoPath := filepath.Join(td.dir, "info", name+".trashinfo")
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
			(&url.URL{Path: stored}).EscapedPath(), time.Now().Format(trashDateLayout))
		cerr := f.Close()
		if werr != nil || cerr != nil {
			os.Remove(infoPath)
			return "", errors.Join(werr, cerr)
		}
		if err := os.Rename(path, filepath.Join(td.dir, "files", name)); err != nil {
			os.Remove(infoPath)
			if errors.Is(err, syscall.EXDEV) {
				return "", ErrOtherDrive
			}
			return "", err
		}
		return td.name(name), nil
	}
	return "", errors.New("too many items with the same name in the trash")
}

// List returns the items in all trashes, newest first.
func (t *Trash) List() ([]TrashItem, error) {
	items := []TrashItem{}
	for i, td := range t.all() {
		infos, err := os.ReadDir(filepath.Join(td.dir, "info"))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			if i == 0 {
				return nil, err
			}
			continue // an unreadable drive trash hides only its items
		}
		for _, de := range infos {
			name, ok := strings.CutSuffix(de.Name(), ".trashinfo")
			if !ok || !validName(name) {
				continue
			}
			orig, deleted, err := td.readInfo(name)
			if err != nil {
				continue
			}
			filePath := filepath.Join(td.dir, "files", name)
			lst, err := os.Lstat(filePath)
			if err != nil {
				continue // orphaned .trashinfo
			}
			items = append(items, TrashItem{
				Name:         td.name(name),
				OriginalPath: orig,
				DeletedAt:    deleted,
				Entry:        entryFor(filePath, lst),
			})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].DeletedAt.After(items[j].DeletedAt) })
	return items, nil
}

func (td trashDir) readInfo(name string) (string, time.Time, error) {
	f, err := os.Open(filepath.Join(td.dir, "info", name+".trashinfo"))
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
	switch {
	case filepath.IsAbs(orig):
		orig = filepath.Clean(orig)
	case td.top != "":
		// Relative to the drive's top folder, and must stay on it.
		orig = filepath.Join(td.top, orig)
		if rel, err := filepath.Rel(td.top, orig); err != nil || strings.HasPrefix(rel, "..") {
			return "", time.Time{}, errors.New("trashinfo path leaves the drive")
		}
	default:
		// Relative paths in the home trash are relative to the directory
		// holding it.
		orig = filepath.Join(filepath.Dir(td.dir), orig)
	}
	return orig, deleted, nil
}

// Restore moves an item back to where it was deleted from.
func (t *Trash) Restore(name string) (string, error) {
	td, base, err := t.locate(name)
	if err != nil {
		return "", err
	}
	orig, _, err := td.readInfo(base)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(orig); err == nil {
		return "", fs.ErrExist
	}
	if err := os.MkdirAll(filepath.Dir(orig), 0o777); err != nil {
		return "", err
	}
	if err := os.Rename(filepath.Join(td.dir, "files", base), orig); err != nil {
		return "", err
	}
	os.Remove(filepath.Join(td.dir, "info", base+".trashinfo"))
	return orig, nil
}

// Empty permanently deletes everything in all trashes.
func (t *Trash) Empty() error {
	var errs []error
	for _, td := range t.all() {
		for _, d := range []string{"files", "info"} {
			dir := filepath.Join(td.dir, d)
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
		os.Remove(filepath.Join(td.dir, "directorysizes"))
	}
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
	var trashed []string
	for _, res := range results {
		if res.Error == "" {
			trashed = append(trashed, res.Path)
		}
	}
	if len(trashed) > 0 {
		paths, n := auditPaths(trashed)
		w.audit(r, protocol.AuditEvent{Event: "trash", Paths: paths, Count: n})
	}
	writeJSON(rw, http.StatusOK, map[string]any{"results": results})
}

func (w *Worker) handleTrashList(rw http.ResponseWriter, r *http.Request) {
	items, err := w.trash.List()
	if err != nil {
		writeErr(rw, err)
		return
	}
	writeJSON(rw, http.StatusOK, map[string]any{"dir": w.trash.Dir(), "filesDirs": w.trash.FilesDirs(), "items": items})
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
	// What was in the trash, for the audit log.
	var originals []string
	if items, err := w.trash.List(); err == nil {
		for _, it := range items {
			originals = append(originals, it.OriginalPath)
		}
	}
	if err := w.trash.Empty(); err != nil {
		writeErr(rw, err)
		return
	}
	paths, n := auditPaths(originals)
	w.audit(r, protocol.AuditEvent{Event: "empty_trash", Paths: paths, Count: n})
	rw.WriteHeader(http.StatusNoContent)
}
