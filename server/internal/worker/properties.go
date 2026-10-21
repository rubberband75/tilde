package worker

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// Properties is what the Properties dialog shows beyond an Entry.
type Properties struct {
	Entry Entry `json:"entry"`
	// Owner and Group are names, or the numeric id if unknown.
	Owner string    `json:"owner"`
	Group string    `json:"group"`
	ATime time.Time `json:"atime"`
	CTime time.Time `json:"ctime"`
	// Mine is true if the user owns the item, so may change its permissions.
	Mine bool `json:"mine"`
	// Free and Capacity describe the filesystem the item is on.
	Free     uint64 `json:"free"`
	Capacity uint64 `json:"capacity"`
}

func (w *Worker) handleProperties(rw http.ResponseWriter, r *http.Request) {
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
	writeJSON(rw, http.StatusOK, w.properties(p, lst))
}

func (w *Worker) properties(p string, lst fs.FileInfo) Properties {
	props := Properties{Entry: entryFor(p, lst)}
	if st, ok := lst.Sys().(*syscall.Stat_t); ok {
		props.Owner = userName(st.Uid)
		props.Group = groupName(st.Gid)
		props.ATime = time.Unix(st.Atim.Sec, st.Atim.Nsec).UTC()
		props.CTime = time.Unix(st.Ctim.Sec, st.Ctim.Nsec).UTC()
		props.Mine = st.Uid == w.info.UID
	}
	var sfs syscall.Statfs_t
	if syscall.Statfs(p, &sfs) == nil {
		props.Free = sfs.Bavail * uint64(sfs.Bsize)
		props.Capacity = sfs.Blocks * uint64(sfs.Bsize)
	}
	return props
}

func userName(uid uint32) string {
	if u, err := user.LookupId(strconv.FormatUint(uint64(uid), 10)); err == nil {
		return u.Username
	}
	return strconv.FormatUint(uint64(uid), 10)
}

func groupName(gid uint32) string {
	if g, err := user.LookupGroupId(strconv.FormatUint(uint64(gid), 10)); err == nil {
		return g.Name
	}
	return strconv.FormatUint(uint64(gid), 10)
}

// handleChmod changes the nine permission bits of a file or folder. The
// setuid, setgid and sticky bits are kept as they are. The kernel allows
// this only for the owner.
func (w *Worker) handleChmod(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
		Perm uint32 `json:"perm"`
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
	if req.Perm > 0o777 {
		writeErr(rw, badRequest{"perm must be between 0 and 0777"})
		return
	}
	lst, err := os.Lstat(p)
	if err != nil {
		writeErr(rw, err)
		return
	}
	if lst.Mode()&fs.ModeSymlink != 0 {
		writeErr(rw, badRequest{"links have no permissions of their own"})
		return
	}
	special := lst.Mode() & (fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky)
	if err := os.Chmod(p, fs.FileMode(req.Perm)|special); err != nil {
		writeErr(rw, err)
		return
	}
	lst, err = os.Lstat(p)
	if err != nil {
		writeErr(rw, err)
		return
	}
	writeJSON(rw, http.StatusOK, entryFor(p, lst))
}

// handleUsage counts what is in the given paths (repeat path=), streaming
// progress as NDJSON every 250 ms: {"items","size","unreadable","done"}.
// items counts everything below the paths (not the paths themselves), size
// adds up file sizes. Like du -x it stays on each path's filesystem and
// does not follow links. Closing the request stops the count.
func (w *Worker) handleUsage(rw http.ResponseWriter, r *http.Request) {
	var roots []string
	for _, raw := range r.URL.Query()["path"] {
		p, err := cleanAbs(raw)
		if err != nil {
			writeErr(rw, err)
			return
		}
		roots = append(roots, p)
	}
	if len(roots) == 0 {
		writeErr(rw, badRequest{"missing path"})
		return
	}
	rw.Header().Set("Content-Type", "application/x-ndjson")
	rw.Header().Set("Cache-Control", "no-store")
	flusher, _ := rw.(http.Flusher)
	enc := json.NewEncoder(rw)
	var u struct {
		Items      int64 `json:"items"`
		Size       int64 `json:"size"`
		Unreadable int64 `json:"unreadable"`
		Done       bool  `json:"done"`
	}
	last := time.Now()
	report := func() bool {
		if enc.Encode(u) != nil {
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		last = time.Now()
		return true
	}
	stop := errors.New("stop")
	for _, root := range roots {
		st, err := os.Lstat(root)
		if err != nil {
			u.Unreadable++
			continue
		}
		if !st.IsDir() {
			if st.Mode().IsRegular() {
				u.Size += st.Size()
			}
			continue
		}
		var dev uint64
		if sys, ok := st.Sys().(*syscall.Stat_t); ok {
			dev = uint64(sys.Dev)
		}
		err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if r.Context().Err() != nil {
				return stop
			}
			if err != nil {
				u.Unreadable++
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if p == root {
				return nil
			}
			info, ierr := d.Info()
			if ierr != nil {
				u.Unreadable++
				return nil
			}
			u.Items++
			if info.Mode().IsRegular() {
				u.Size += info.Size()
			}
			if d.IsDir() {
				if sys, ok := info.Sys().(*syscall.Stat_t); ok && uint64(sys.Dev) != dev {
					return fs.SkipDir
				}
			}
			if time.Since(last) > 250*time.Millisecond && !report() {
				return stop
			}
			return nil
		})
		if errors.Is(err, stop) {
			return
		}
	}
	u.Done = true
	report()
}
