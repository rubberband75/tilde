package worker

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// maxSearchResults caps a search; the last line says when it was cut short.
const maxSearchResults = 5000

// handleSearch finds files and folders under path whose names contain every
// word of q (case-insensitive), as Nemo's search bar does. Results stream as
// NDJSON like a folder listing: {"entries":[...]} lines, then
// {"done":true,"total":n,"truncated":bool}. Hidden items are skipped unless
// hidden=1. Links are not followed and the walk stays on path's filesystem
// (like find -xdev), so searching / does not wander into /proc or network
// mounts. Closing the request stops the walk.
func (w *Worker) handleSearch(rw http.ResponseWriter, r *http.Request) {
	root, err := queryPath(r, "path")
	if err != nil {
		writeErr(rw, err)
		return
	}
	terms := strings.Fields(strings.ToLower(r.URL.Query().Get("q")))
	if len(terms) == 0 {
		writeErr(rw, badRequest{"empty search"})
		return
	}
	hidden := r.URL.Query().Get("hidden") == "1"
	var rootDev uint64
	if st, err := os.Stat(root); err != nil {
		writeErr(rw, err)
		return
	} else if !st.IsDir() {
		writeErr(rw, syscall.ENOTDIR)
		return
	} else if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		rootDev = uint64(sys.Dev)
	}

	rw.Header().Set("Content-Type", "application/x-ndjson")
	rw.Header().Set("Cache-Control", "no-store")
	flusher, _ := rw.(http.Flusher)
	enc := json.NewEncoder(rw)
	var batch []Entry
	total := 0
	lastFlush := time.Now()
	flush := func() bool {
		if len(batch) > 0 {
			if enc.Encode(map[string]any{"entries": batch}) != nil {
				return false
			}
			batch = batch[:0]
		}
		if flusher != nil {
			flusher.Flush()
		}
		lastFlush = time.Now()
		return true
	}
	budget := sniffBudget
	errStop := errors.New("stop")

	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if r.Context().Err() != nil {
			return errStop
		}
		if err != nil {
			// Unreadable folders are skipped, as find does.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if p == root {
			return nil
		}
		name := d.Name()
		isHidden := strings.HasPrefix(name, ".")
		if isHidden && !hidden {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		if d.IsDir() {
			if sys, ok := info.Sys().(*syscall.Stat_t); ok && uint64(sys.Dev) != rootDev {
				return fs.SkipDir
			}
		}
		if matches(strings.ToLower(name), terms) {
			batch = append(batch, entryWithBudget(p, info, &budget))
			total++
			if total >= maxSearchResults {
				return errStop
			}
		}
		if len(batch) >= 200 || (len(batch) > 0 && time.Since(lastFlush) > 200*time.Millisecond) {
			if !flush() {
				return errStop
			}
		}
		return nil
	})
	if r.Context().Err() != nil {
		return
	}
	if !flush() {
		return
	}
	enc.Encode(map[string]any{"done": true, "total": total, "truncated": total >= maxSearchResults})
}

func matches(name string, terms []string) bool {
	for _, t := range terms {
		if !strings.Contains(name, t) {
			return false
		}
	}
	return true
}
