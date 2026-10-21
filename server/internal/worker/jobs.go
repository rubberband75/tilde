package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// File-operation jobs (D31): copy, move and delete whole trees in the
// background, with progress, cancel and a conflict prompt that any of the
// user's clients can answer. Every change is published on the events
// WebSocket as a full snapshot of the job.

// Job operations.
const (
	OpCopy   = "copy"
	OpMove   = "move"
	OpDrop   = "drop" // drag and drop: move within a filesystem, copy across (as Nemo)
	OpDelete = "delete"
)

// Job states.
const (
	JobPreparing = "preparing" // counting files
	JobRunning   = "running"
	JobWaiting   = "waiting" // a conflict needs an answer
	JobDone      = "done"    // finished; Errors lists items that failed
	JobFailed    = "failed"  // could not run at all; see Error
	JobCancelled = "cancelled"
)

// Conflict actions.
const (
	ActReplace = "replace"
	ActSkip    = "skip"
	ActRename  = "rename"
	ActMerge   = "merge"
)

// maxJobErrors bounds the error list; ErrorCount keeps the full count.
const maxJobErrors = 100

// copyChunk is how much a file copy moves between progress updates and
// cancellation checks. os.File.ReadFrom still uses copy_file_range inside
// each chunk.
const copyChunk = 8 << 20

// JobInfo is the JSON view of a job. Events always carry the whole job, so
// a client that missed some is never out of step.
type JobInfo struct {
	ID         string     `json:"id"`
	Op         string     `json:"op"`
	Sources    []string   `json:"sources"`
	Dest       string     `json:"dest,omitempty"`
	State      string     `json:"state"`
	Current    string     `json:"current,omitempty"`
	TotalItems int64      `json:"totalItems"`
	DoneItems  int64      `json:"doneItems"`
	TotalBytes int64      `json:"totalBytes"`
	DoneBytes  int64      `json:"doneBytes"`
	Conflict   *Conflict  `json:"conflict,omitempty"`
	Errors     []JobError `json:"errors"`
	ErrorCount int        `json:"errorCount"`
	Error      string     `json:"error,omitempty"`
	Started    time.Time  `json:"started"`
	Finished   *time.Time `json:"finished,omitempty"`
}

// Conflict is an item that already exists at the destination.
type Conflict struct {
	ID     int   `json:"id"`
	Source Entry `json:"source"`
	Dest   Entry `json:"dest"`
	// Suggested is a free name for "rename".
	Suggested string `json:"suggested"`
	// Actions lists the answers that make sense: folders merge, files
	// replace, and nothing replaces a folder.
	Actions []string `json:"actions"`
}

// JobError is one item that could not be copied, moved or deleted. Jobs do
// not stop for these; the list is shown when the job ends.
type JobError struct {
	Path  string `json:"path"`
	Error string `json:"error"`
	Code  string `json:"code"`
}

// Resolution answers a conflict.
type Resolution struct {
	Conflict   int    `json:"conflict"`
	Action     string `json:"action"`
	Name       string `json:"name,omitempty"` // new name for "rename"
	ApplyToAll bool   `json:"applyToAll"`
}

var (
	errIntoItself = errors.New("cannot copy or move a folder into itself")
	errSpecial    = errors.New("special files (devices, sockets, pipes) are not copied")
)

// transfer modes for one item.
type mode int

const (
	modeCopy       mode = iota // copy
	modeMove                   // rename; on another filesystem, copy then delete
	modeDrop                   // rename; on another filesystem, copy
	modeCopyDelete             // copy, deleting each source item once copied
)

type job struct {
	m       *jobManager
	ctx     context.Context
	cancel  context.CancelFunc
	answers chan Resolution
	// always holds "apply to all" answers by conflict kind ("dir", "file").
	always map[string]Resolution
	// conflicts numbers conflicts so a late answer cannot hit the next one.
	// Only the job's own goroutine uses it.
	conflicts int

	mu    sync.Mutex
	info  JobInfo
	dirty bool
}

func (j *job) snapshot() JobInfo {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.snapshotLocked()
}

func (j *job) snapshotLocked() JobInfo {
	in := j.info
	in.Sources = append([]string(nil), j.info.Sources...)
	in.Errors = append([]JobError{}, j.info.Errors...)
	if j.info.Conflict != nil {
		c := *j.info.Conflict
		in.Conflict = &c
	}
	return in
}

// update changes the job. Progress is published by the ticker in run;
// state changes (now) are published at once.
func (j *job) update(now bool, fn func(*JobInfo)) {
	j.mu.Lock()
	fn(&j.info)
	j.dirty = true
	j.mu.Unlock()
	if now {
		j.publish()
	}
}

func (j *job) publish() {
	j.mu.Lock()
	j.dirty = false
	snap := j.snapshotLocked()
	j.mu.Unlock()
	j.m.hub.publish(event{Type: "job", Job: &snap})
}

func (j *job) publishIfDirty() {
	j.mu.Lock()
	dirty := j.dirty
	j.mu.Unlock()
	if dirty {
		j.publish()
	}
}

func (j *job) fail(path string, err error) {
	_, code := classifyErr(err)
	j.update(false, func(in *JobInfo) {
		in.ErrorCount++
		if len(in.Errors) < maxJobErrors {
			in.Errors = append(in.Errors, JobError{Path: path, Error: err.Error(), Code: code})
		}
	})
}

func (j *job) setCurrent(p string) { j.update(false, func(in *JobInfo) { in.Current = p }) }
func (j *job) itemDone()           { j.update(false, func(in *JobInfo) { in.DoneItems++ }) }
func (j *job) addBytes(n int64)    { j.update(false, func(in *JobInfo) { in.DoneBytes += n }) }

func (j *job) addTotals(items, bytes int64) {
	j.update(false, func(in *JobInfo) {
		in.TotalItems += items
		in.TotalBytes += bytes
	})
}

func (j *job) run() {
	stop := make(chan struct{})
	ticked := make(chan struct{})
	go func() {
		defer close(ticked)
		t := time.NewTicker(150 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				j.publishIfDirty()
			}
		}
	}()

	var err error
	if j.info.Op == OpDelete {
		err = j.runDelete()
	} else {
		err = j.runTransfer()
	}
	close(stop)
	<-ticked

	now := time.Now()
	j.update(true, func(in *JobInfo) {
		in.Current = ""
		in.Conflict = nil
		in.Finished = &now
		switch {
		case j.ctx.Err() != nil:
			in.State = JobCancelled
		case err != nil:
			in.State = JobFailed
			in.Error = err.Error()
		default:
			in.State = JobDone
		}
	})
	j.cancel()
}

// --- copy, move, drop ---

func (j *job) runTransfer() error {
	dest := j.info.Dest
	st, err := os.Stat(dest)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return badRequest{"the destination is not a folder"}
	}
	m := modeCopy
	switch j.info.Op {
	case OpMove:
		m = modeMove
	case OpDrop:
		m = modeDrop
	}
	if m == modeCopy {
		j.update(true, func(in *JobInfo) { in.State = JobPreparing })
		items, bytes := j.scan(j.info.Sources)
		if err := j.ctx.Err(); err != nil {
			return err
		}
		j.addTotals(items, bytes)
	} else {
		// Renames are instant; byte totals are only counted for items that
		// turn out to be on another filesystem.
		j.addTotals(int64(len(j.info.Sources)), 0)
	}
	j.update(true, func(in *JobInfo) { in.State = JobRunning })

	for _, src := range j.info.Sources {
		if err := j.ctx.Err(); err != nil {
			return err
		}
		if src == dest || strings.HasPrefix(dest+"/", src+"/") {
			j.fail(src, errIntoItself)
			continue
		}
		target := filepath.Join(dest, filepath.Base(src))
		if filepath.Dir(src) == dest {
			if m != modeCopy {
				j.itemDone() // moving a file onto its own folder: nothing to do
				continue
			}
			// Copying into the same folder makes "name (copy)", like Nemo.
			isDir := false
			if lst, err := os.Lstat(src); err == nil {
				isDir = lst.IsDir()
			}
			target = filepath.Join(dest, freeName(dest, filepath.Base(src), isDir, true))
		}
		j.transfer(src, target, m)
	}
	return j.ctx.Err()
}

// transfer copies or moves src to dst, asking about an existing dst.
func (j *job) transfer(src, dst string, m mode) {
	if j.ctx.Err() != nil {
		return
	}
	j.setCurrent(src)
	slst, err := os.Lstat(src)
	if err != nil {
		j.fail(src, err)
		return
	}
	replace := false
	if dlst, err := os.Lstat(dst); err == nil {
		res, err := j.resolve(src, slst, dst, dlst)
		if err != nil {
			return // cancelled
		}
		switch res.Action {
		case ActSkip:
			j.skip(src, slst, m)
			return
		case ActRename:
			j.transfer(src, filepath.Join(filepath.Dir(dst), res.Name), m)
			return
		case ActMerge:
			j.merge(src, dst, m)
			return
		case ActReplace:
			replace = true
			// A file can replace a file in one atomic rename; anything else
			// (a folder replacing a file) needs the old item gone first.
			if slst.IsDir() || dlst.Mode().Type() != slst.Mode().Type() {
				if err := os.Remove(dst); err != nil {
					j.fail(dst, err)
					return
				}
				replace = false
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		j.fail(dst, err)
		return
	}

	if m == modeMove || m == modeDrop {
		var err error
		if replace {
			err = os.Rename(src, dst)
		} else {
			err = renameNoReplace(src, dst)
		}
		if err == nil {
			j.itemDone()
			return
		}
		if !errors.Is(err, syscall.EXDEV) {
			j.fail(src, err)
			return
		}
		// Another filesystem: copy it (and for a move, delete as we go).
		items, bytes := j.scan([]string{src})
		j.addTotals(items-1, bytes)
		if m == modeMove {
			m = modeCopyDelete
		} else {
			m = modeCopy
		}
	}
	j.copyItem(src, slst, dst, replace, m)
}

// copyItem copies one item (recursively for folders) to a dst that does not
// exist, or that is a file being replaced.
func (j *job) copyItem(src string, slst fs.FileInfo, dst string, replace bool, m mode) {
	del := m == modeCopyDelete
	switch {
	case slst.Mode()&fs.ModeSymlink != 0:
		// Links are copied as links, never followed (D31).
		target, err := os.Readlink(src)
		if err != nil {
			j.fail(src, err)
			return
		}
		if replace {
			os.Remove(dst)
		}
		if err := os.Symlink(target, dst); err != nil {
			j.fail(dst, err)
			return
		}
		j.itemDone()
	case slst.IsDir():
		// Created writable so the copy can be filled even when the source
		// folder is read-only; the real mode is applied afterwards.
		if err := os.Mkdir(dst, 0o700); err != nil {
			j.fail(dst, err)
			return
		}
		names, err := readNames(src)
		if err != nil {
			j.fail(src, err)
		}
		for _, n := range names {
			j.transfer(filepath.Join(src, n), filepath.Join(dst, n), m)
		}
		os.Chmod(dst, slst.Mode().Perm())
		os.Chtimes(dst, time.Time{}, slst.ModTime())
		j.itemDone()
	case slst.Mode().IsRegular():
		if err := j.copyFile(src, slst, dst, replace); err != nil {
			if !errors.Is(err, context.Canceled) {
				j.fail(src, err)
			}
			return
		}
		j.itemDone()
	default:
		j.fail(src, errSpecial)
		return
	}
	if del && j.ctx.Err() == nil {
		// A folder that still holds skipped or failed items stays.
		if err := os.Remove(src); err != nil && !errors.Is(err, syscall.ENOTEMPTY) {
			j.fail(src, err)
		}
	}
}

// copyFile copies a regular file through a temporary file in the target
// folder, so a cancelled or failed copy never leaves a partial file under
// the real name (and a replaced file stays intact until the copy is done).
func (j *job) copyFile(src string, st fs.FileInfo, dst string, replace bool) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".tilde-copy-*")
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	for {
		if err := j.ctx.Err(); err != nil {
			return err
		}
		n, err := io.CopyN(tmp, in, copyChunk)
		j.addBytes(n)
		if testHookCopyChunk != nil {
			testHookCopyChunk(j)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Permission bits only: like cp without -p, setuid/setgid are dropped.
	if err := os.Chmod(tmp.Name(), st.Mode().Perm()); err != nil {
		return err
	}
	os.Chtimes(tmp.Name(), time.Time{}, st.ModTime())
	if err := commitFile(tmp.Name(), dst, replace); err != nil {
		return err
	}
	committed = true
	return nil
}

// testHookCopyChunk runs after every chunk of a file copy (tests only).
var testHookCopyChunk func(*job)

// testHookRemove runs before each item a delete visits (tests only).
var testHookRemove func(path string)

// merge moves or copies the contents of folder src into existing folder dst.
func (j *job) merge(src, dst string, m mode) {
	names, err := readNames(src)
	if err != nil {
		j.fail(src, err)
		return
	}
	for _, n := range names {
		j.transfer(filepath.Join(src, n), filepath.Join(dst, n), m)
	}
	if m != modeCopy && j.ctx.Err() == nil {
		// The source folder goes once everything in it has moved.
		if err := os.Remove(src); err != nil && !errors.Is(err, syscall.ENOTEMPTY) {
			j.fail(src, err)
		}
	}
	j.itemDone()
}

// skip counts a skipped item as done so the progress still reaches the end.
func (j *job) skip(src string, slst fs.FileInfo, m mode) {
	if m != modeCopy && m != modeCopyDelete {
		j.itemDone()
		return
	}
	items, bytes := j.scan([]string{src})
	j.update(false, func(in *JobInfo) {
		in.DoneItems += items
		in.DoneBytes += bytes
	})
}

// resolve asks the user what to do about an existing dst, unless an earlier
// answer applies to all conflicts of this kind.
func (j *job) resolve(src string, slst fs.FileInfo, dst string, dlst fs.FileInfo) (Resolution, error) {
	kind := "file"
	actions := []string{ActReplace, ActSkip, ActRename}
	switch {
	case slst.IsDir() && dlst.IsDir():
		kind = "dir"
		actions = []string{ActMerge, ActSkip, ActRename}
	case dlst.IsDir():
		actions = []string{ActSkip, ActRename}
	}
	suggested := freeName(filepath.Dir(dst), filepath.Base(dst), slst.IsDir(), false)
	if r, ok := j.always[kind]; ok && contains(actions, r.Action) {
		if r.Action == ActRename {
			r.Name = suggested
		}
		return r, nil
	}

	j.conflicts++
	id := j.conflicts
	j.update(true, func(in *JobInfo) {
		in.State = JobWaiting
		in.Conflict = &Conflict{
			ID:        id,
			Source:    entryFor(src, slst),
			Dest:      entryFor(dst, dlst),
			Suggested: suggested,
			Actions:   actions,
		}
	})
	for {
		select {
		case <-j.ctx.Done():
			return Resolution{}, j.ctx.Err()
		case r := <-j.answers:
			if r.Conflict != id || !contains(actions, r.Action) {
				continue // stale answer from another client
			}
			if r.ApplyToAll {
				j.always[kind] = r
			}
			j.update(true, func(in *JobInfo) {
				in.State = JobRunning
				in.Conflict = nil
			})
			return r, nil
		}
	}
}

// --- delete ---

func (j *job) runDelete() error {
	j.update(true, func(in *JobInfo) { in.State = JobPreparing })
	items, bytes := j.scan(j.info.Sources)
	if err := j.ctx.Err(); err != nil {
		return err
	}
	j.addTotals(items, bytes)
	j.update(true, func(in *JobInfo) { in.State = JobRunning })
	for _, src := range j.info.Sources {
		if err := j.ctx.Err(); err != nil {
			return err
		}
		if src == "/" {
			j.fail(src, badRequest{"refusing to delete /"})
			continue
		}
		if j.remove(src) && filepath.Dir(src) == j.m.trashFiles {
			// Deleting from the trash removes its .trashinfo too.
			os.Remove(filepath.Join(filepath.Dir(j.m.trashFiles), "info", filepath.Base(src)+".trashinfo"))
		}
	}
	return j.ctx.Err()
}

// remove deletes p and everything under it, reporting whether all of it
// went. It works through directory file descriptors (openat with
// O_NOFOLLOW, unlinkat), as os.RemoveAll does, so if someone swaps a folder
// inside the tree for a symlink while it is being deleted, the delete does
// not follow the link into another tree (the classic rm -rf race).
func (j *job) remove(p string) bool {
	parent, err := unix.Open(filepath.Dir(p), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		j.fail(p, &fs.PathError{Op: "open", Path: filepath.Dir(p), Err: err})
		return false
	}
	defer unix.Close(parent)
	return j.removeAt(parent, filepath.Base(p), p)
}

// removeAt deletes name in the folder open as dirfd; p is its full path,
// for progress and errors only.
func (j *job) removeAt(dirfd int, name, p string) bool {
	if j.ctx.Err() != nil {
		return false
	}
	j.setCurrent(p)
	if testHookRemove != nil {
		testHookRemove(p)
	}
	var st unix.Stat_t
	if err := unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		j.fail(p, &fs.PathError{Op: "lstat", Path: p, Err: err})
		return false
	}
	isDir := st.Mode&unix.S_IFMT == unix.S_IFDIR
	if isDir {
		fd, err := unix.Openat(dirfd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			j.fail(p, &fs.PathError{Op: "open", Path: p, Err: err})
			return false
		}
		dir := os.NewFile(uintptr(fd), p)
		names, err := dir.Readdirnames(-1)
		if err != nil {
			dir.Close()
			j.fail(p, err)
			return false
		}
		sort.Strings(names)
		ok := true
		for _, n := range names {
			if !j.removeAt(fd, n, filepath.Join(p, n)) {
				ok = false
			}
		}
		dir.Close()
		if !ok {
			return false
		}
	}
	flags := 0
	if isDir {
		flags = unix.AT_REMOVEDIR
	}
	if err := unix.Unlinkat(dirfd, name, flags); err != nil {
		j.fail(p, &fs.PathError{Op: "remove", Path: p, Err: err})
		return false
	}
	j.update(false, func(in *JobInfo) {
		in.DoneItems++
		if st.Mode&unix.S_IFMT == unix.S_IFREG {
			in.DoneBytes += st.Size
		}
	})
	return true
}

// --- helpers ---

// scan counts items and file bytes under paths without following links,
// updating the totals as it goes so the UI can show "Preparing (n files)".
func (j *job) scan(paths []string) (items, bytes int64) {
	var walk func(p string)
	walk = func(p string) {
		if j.ctx.Err() != nil {
			return
		}
		lst, err := os.Lstat(p)
		if err != nil {
			return
		}
		items++
		if lst.Mode().IsRegular() {
			bytes += lst.Size()
		}
		if lst.IsDir() {
			names, _ := readNames(p)
			for _, n := range names {
				walk(filepath.Join(p, n))
			}
		}
	}
	for _, p := range paths {
		walk(p)
	}
	return items, bytes
}

func readNames(dir string) ([]string, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	names, err := f.Readdirnames(-1)
	sort.Strings(names)
	return names, err
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// renameNoReplace renames src to dst and fails with fs.ErrExist instead of
// replacing an existing dst, without the race of checking first.
func renameNoReplace(src, dst string) error {
	err := unix.Renameat2(unix.AT_FDCWD, src, unix.AT_FDCWD, dst, unix.RENAME_NOREPLACE)
	if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOSYS) {
		// Filesystems without RENAME_NOREPLACE (some network and FUSE
		// mounts): check, then rename.
		if _, err := os.Lstat(dst); err == nil {
			return &os.LinkError{Op: "rename", Old: src, New: dst, Err: fs.ErrExist}
		}
		return os.Rename(src, dst)
	}
	if err != nil {
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: err}
	}
	return nil
}

var copySuffixRe = regexp.MustCompile(` \((copy|copy \d+|\d+)\)$`)

// freeName returns a name not yet used in dir, based on name: "a (copy).txt",
// "a (copy 2).txt" for duplicates; "a (2).txt", "a (3).txt" for conflicts.
func freeName(dir, name string, isDir, duplicate bool) string {
	stem, ext := name, ""
	if !isDir {
		stem, ext = splitExt(name)
	}
	stem = copySuffixRe.ReplaceAllString(stem, "")
	for i := 1; i < 100000; i++ {
		var label string
		switch {
		case duplicate && i == 1:
			label = "copy"
		case duplicate:
			label = fmt.Sprintf("copy %d", i)
		default:
			label = fmt.Sprint(i + 1)
		}
		cand := fmt.Sprintf("%s (%s)%s", stem, label, ext)
		if _, err := os.Lstat(filepath.Join(dir, cand)); errors.Is(err, fs.ErrNotExist) {
			return cand
		}
	}
	return fmt.Sprintf("%s (%d)%s", stem, time.Now().UnixNano(), ext)
}

// splitExt splits "a.tar.gz" into "a" and ".tar.gz", and keeps dotfiles
// such as ".bashrc" whole.
func splitExt(name string) (string, string) {
	i := strings.LastIndexByte(name, '.')
	if i <= 0 {
		return name, ""
	}
	if j := strings.LastIndexByte(name[:i], '.'); j > 0 && strings.EqualFold(name[j:i], ".tar") {
		i = j
	}
	return name[:i], name[i:]
}

// --- manager and HTTP API ---

type jobManager struct {
	hub        *eventHub
	trashFiles string

	mu   sync.Mutex
	next int
	jobs map[string]*job
}

func newJobManager(hub *eventHub, trashFiles string) *jobManager {
	return &jobManager{hub: hub, trashFiles: trashFiles, jobs: map[string]*job{}}
}

func (m *jobManager) start(op string, sources []string, dest string) *job {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.next++
	id := fmt.Sprintf("j%d", m.next)
	j := &job{
		m:       m,
		ctx:     ctx,
		cancel:  cancel,
		answers: make(chan Resolution, 1),
		always:  map[string]Resolution{},
		info: JobInfo{
			ID: id, Op: op, Sources: sources, Dest: dest,
			State: JobPreparing, Errors: []JobError{}, Started: time.Now(),
		},
	}
	m.jobs[id] = j
	m.mu.Unlock()
	m.prune()
	j.publish()
	go j.run()
	return j
}

func (m *jobManager) get(id string) *job {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.jobs[id]
}

func (m *jobManager) list() []JobInfo {
	m.mu.Lock()
	all := make([]*job, 0, len(m.jobs))
	for _, j := range m.jobs {
		all = append(all, j)
	}
	m.mu.Unlock()
	out := make([]JobInfo, 0, len(all))
	for _, j := range all {
		out = append(out, j.snapshot())
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Started.Before(out[b].Started) })
	return out
}

// remove forgets a finished job (the user dismissed it).
func (m *jobManager) remove(id string) bool {
	m.mu.Lock()
	j, ok := m.jobs[id]
	if ok && j.snapshot().Finished == nil {
		m.mu.Unlock()
		return false
	}
	delete(m.jobs, id)
	m.mu.Unlock()
	if ok {
		m.hub.publish(event{Type: "job-removed", ID: id})
	}
	return true
}

// prune forgets finished jobs after an hour, so the list stays short even
// if nobody dismisses them.
func (m *jobManager) prune() {
	cutoff := time.Now().Add(-time.Hour)
	var gone []string
	m.mu.Lock()
	for id, j := range m.jobs {
		if f := j.snapshot().Finished; f != nil && f.Before(cutoff) {
			delete(m.jobs, id)
			gone = append(gone, id)
		}
	}
	m.mu.Unlock()
	for _, id := range gone {
		m.hub.publish(event{Type: "job-removed", ID: id})
	}
}

// cancelAll stops every job (logout) and waits briefly for them to clean up
// their temporary files.
func (m *jobManager) cancelAll() {
	m.mu.Lock()
	all := make([]*job, 0, len(m.jobs))
	for _, j := range m.jobs {
		all = append(all, j)
	}
	m.mu.Unlock()
	deadline := time.Now().Add(3 * time.Second)
	for _, j := range all {
		j.cancel()
	}
	for _, j := range all {
		for j.snapshot().Finished == nil && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func (w *Worker) handleJobStart(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		Op      string   `json:"op"`
		Sources []string `json:"sources"`
		Dest    string   `json:"dest"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(rw, err)
		return
	}
	switch req.Op {
	case OpCopy, OpMove, OpDrop, OpDelete:
	default:
		writeErr(rw, badRequest{"unknown op " + req.Op})
		return
	}
	if len(req.Sources) == 0 {
		writeErr(rw, badRequest{"no sources"})
		return
	}
	sources := make([]string, 0, len(req.Sources))
	for _, s := range req.Sources {
		p, err := cleanAbs(s)
		if err != nil {
			writeErr(rw, err)
			return
		}
		sources = append(sources, p)
	}
	dest := ""
	if req.Op != OpDelete {
		p, err := cleanAbs(req.Dest)
		if err != nil {
			writeErr(rw, err)
			return
		}
		dest = p
	}
	j := w.jobs.start(req.Op, sources, dest)
	writeJSON(rw, http.StatusAccepted, j.snapshot())
}

func (w *Worker) handleJobList(rw http.ResponseWriter, r *http.Request) {
	writeJSON(rw, http.StatusOK, map[string]any{"jobs": w.jobs.list()})
}

func (w *Worker) jobOr404(rw http.ResponseWriter, r *http.Request) *job {
	j := w.jobs.get(r.PathValue("id"))
	if j == nil {
		writeJSON(rw, http.StatusNotFound, apiError{Error: "no such job", Code: "not_found"})
	}
	return j
}

func (w *Worker) handleJobGet(rw http.ResponseWriter, r *http.Request) {
	if j := w.jobOr404(rw, r); j != nil {
		writeJSON(rw, http.StatusOK, j.snapshot())
	}
}

func (w *Worker) handleJobCancel(rw http.ResponseWriter, r *http.Request) {
	if j := w.jobOr404(rw, r); j != nil {
		j.cancel()
		rw.WriteHeader(http.StatusNoContent)
	}
}

func (w *Worker) handleJobDelete(rw http.ResponseWriter, r *http.Request) {
	if w.jobs.get(r.PathValue("id")) == nil {
		rw.WriteHeader(http.StatusNoContent)
		return
	}
	if !w.jobs.remove(r.PathValue("id")) {
		writeJSON(rw, http.StatusConflict, apiError{Error: "the job is still running", Code: "busy"})
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}

// handleJobResolve answers the job's current conflict. With several clients
// open, the first answer wins; a late one gets 409.
func (w *Worker) handleJobResolve(rw http.ResponseWriter, r *http.Request) {
	j := w.jobOr404(rw, r)
	if j == nil {
		return
	}
	var res Resolution
	if err := decodeJSON(r, &res); err != nil {
		writeErr(rw, err)
		return
	}
	j.mu.Lock()
	c := j.info.Conflict
	current := j.info.State == JobWaiting && c != nil && c.ID == res.Conflict
	var actions []string
	if current {
		actions = c.Actions
	}
	j.mu.Unlock()
	if !current {
		writeJSON(rw, http.StatusConflict, apiError{Error: "this conflict was already answered", Code: "stale"})
		return
	}
	if !contains(actions, res.Action) {
		writeErr(rw, badRequest{"action not allowed here: " + res.Action})
		return
	}
	if res.Action == ActRename && !validName(res.Name) {
		writeErr(rw, badRequest{"invalid file name"})
		return
	}
	select {
	case j.answers <- res:
		rw.WriteHeader(http.StatusNoContent)
	default:
		writeJSON(rw, http.StatusConflict, apiError{Error: "this conflict was already answered", Code: "stale"})
	}
}
