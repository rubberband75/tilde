package worker

import (
	"encoding/binary"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// dirWatcher tells clients when a folder they show changes, so the desktop
// and Files windows stay current without polling. Clients name the folders
// they show on the events socket; the worker keeps one inotify watch per
// folder for all of them and publishes the changed folders as "fs" events.
//
// Watches are not recursive: a window shows one folder at a time, and
// recursive inotify costs a watch per subfolder. Events are coalesced for
// watchDelay, so a copy of ten thousand files costs a few reloads a second,
// not ten thousand.
type dirWatcher struct {
	publish func(dirs []string)

	mu sync.Mutex
	// The inotify instance, opened on first use. fd is kept apart because
	// File.Fd would switch the file to blocking mode, and then Close could
	// no longer stop the reader.
	file   *os.File
	fd     int
	refs   map[string]int
	wds    map[string]int              // path → watch descriptor, while watched
	paths  map[int]map[string]struct{} // wd → paths; links can name the same folder
	dirty  map[string]struct{}
	timer  *time.Timer
	closed bool
}

// watchDelay is how long changes are collected before they are published.
const watchDelay = 200 * time.Millisecond

// watchMask asks for changes that alter a listing: entries added, removed or
// renamed, metadata changed, files finished writing, and the folder itself
// going away. Plain reads (IN_ACCESS, IN_CLOSE_NOWRITE) are left out, so the
// worker's own listing and sniffing never trigger events.
const watchMask = unix.IN_CREATE | unix.IN_DELETE | unix.IN_MOVED_FROM | unix.IN_MOVED_TO |
	unix.IN_ATTRIB | unix.IN_CLOSE_WRITE | unix.IN_DELETE_SELF | unix.IN_MOVE_SELF |
	unix.IN_ONLYDIR | unix.IN_EXCL_UNLINK

// maxWatchesPerClient bounds what one client can ask for. A client watches
// the folders of its open windows and the desktop, so this is generous.
const maxWatchesPerClient = 256

func newDirWatcher(publish func(dirs []string)) *dirWatcher {
	return &dirWatcher{
		publish: publish,
		refs:    map[string]int{},
		wds:     map[string]int{},
		paths:   map[int]map[string]struct{}{},
		dirty:   map[string]struct{}{},
	}
}

// add counts one more client watching dir and starts the watch if needed.
func (d *dirWatcher) add(dir string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	d.refs[dir]++
	d.start(dir)
}

// retry starts the watch on a folder clients already want if it is not
// running: it could not be watched before (missing, no permission, out of
// watches), or it was removed and has come back.
func (d *dirWatcher) retry(dir string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.closed && d.refs[dir] > 0 {
		d.start(dir)
	}
}

// start adds the inotify watch for dir unless it has one. Called with mu held.
func (d *dirWatcher) start(dir string) {
	if _, ok := d.wds[dir]; ok {
		return
	}
	if d.file == nil && !d.open() {
		return
	}
	wd, err := unix.InotifyAddWatch(d.fd, dir, watchMask)
	if err != nil {
		slog.Debug("worker: watch folder", "dir", dir, "err", err)
		return
	}
	d.wds[dir] = wd
	if d.paths[wd] == nil {
		d.paths[wd] = map[string]struct{}{}
	}
	d.paths[wd][dir] = struct{}{}
}

// remove undoes one add and drops the watch when no client needs it.
func (d *dirWatcher) remove(dir string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.refs[dir] == 0 {
		return
	}
	if d.refs[dir]--; d.refs[dir] > 0 {
		return
	}
	delete(d.refs, dir)
	wd, ok := d.wds[dir]
	if !ok {
		return
	}
	delete(d.wds, dir)
	delete(d.paths[wd], dir)
	if len(d.paths[wd]) == 0 {
		delete(d.paths, wd)
		if d.file != nil && !d.closed {
			_, _ = unix.InotifyRmWatch(d.fd, uint32(wd))
		}
	}
}

// open starts the inotify instance and its reader. Called with mu held.
func (d *dirWatcher) open() bool {
	// Non-blocking, so the runtime poller owns it and Close stops the reader.
	fd, err := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
	if err != nil {
		slog.Warn("worker: inotify unavailable; folders will not refresh by themselves", "err", err)
		return false
	}
	d.fd = fd
	d.file = os.NewFile(uintptr(fd), "inotify")
	go d.read(d.file)
	return true
}

func (d *dirWatcher) read(f *os.File) {
	buf := make([]byte, 64<<10)
	for {
		n, err := f.Read(buf)
		if err != nil {
			if !errors.Is(err, os.ErrClosed) {
				slog.Error("worker: read inotify", "err", err)
			}
			return
		}
		d.handle(buf[:n])
	}
}

// handle marks the folders named by a batch of raw inotify events as changed.
func (d *dirWatcher) handle(buf []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for len(buf) >= unix.SizeofInotifyEvent {
		wd := int(int32(binary.NativeEndian.Uint32(buf[0:4])))
		mask := binary.NativeEndian.Uint32(buf[4:8])
		nameLen := binary.NativeEndian.Uint32(buf[12:16])
		buf = buf[min(len(buf), unix.SizeofInotifyEvent+int(nameLen)):]

		if mask&unix.IN_Q_OVERFLOW != 0 {
			// Events were lost: every watched folder may have changed.
			for p := range d.wds {
				d.markDirty(p)
			}
			continue
		}
		for p := range d.paths[wd] {
			d.markDirty(p)
		}
		if mask&unix.IN_IGNORED != 0 {
			// The folder is gone (or unmounted). Forget the watch; a client
			// that shows it again re-adds it.
			for p := range d.paths[wd] {
				delete(d.wds, p)
			}
			delete(d.paths, wd)
		}
	}
}

// markDirty queues dir for the next publish. Called with mu held.
func (d *dirWatcher) markDirty(dir string) {
	d.dirty[dir] = struct{}{}
	if d.timer == nil && !d.closed {
		d.timer = time.AfterFunc(watchDelay, d.flush)
	}
}

func (d *dirWatcher) flush() {
	d.mu.Lock()
	dirs := make([]string, 0, len(d.dirty))
	for p := range d.dirty {
		dirs = append(dirs, p)
	}
	clear(d.dirty)
	d.timer = nil
	closed := d.closed
	d.mu.Unlock()
	if len(dirs) > 0 && !closed {
		slices.Sort(dirs)
		d.publish(dirs)
	}
}

// close stops watching everything; the worker is shutting down.
func (d *dirWatcher) close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	if d.timer != nil {
		d.timer.Stop()
	}
	if d.file != nil {
		d.file.Close()
	}
}

// watchSet is one client's watched folders, kept in step with dirWatcher.
type watchSet struct {
	w    *dirWatcher
	dirs map[string]struct{}
}

// set replaces the client's folders with dirs and retries the ones whose
// watch is not running. Relative or unclean paths are ignored, and only the
// first maxWatchesPerClient are kept.
func (s *watchSet) set(dirs []string) {
	next := map[string]struct{}{}
	for _, p := range dirs {
		if len(next) == maxWatchesPerClient {
			break
		}
		if filepath.IsAbs(p) && filepath.Clean(p) == p {
			next[p] = struct{}{}
		}
	}
	for p := range next {
		if _, ok := s.dirs[p]; ok {
			s.w.retry(p)
		} else {
			s.w.add(p)
		}
	}
	for p := range s.dirs {
		if _, ok := next[p]; !ok {
			s.w.remove(p)
		}
	}
	s.dirs = next
}

// clear drops all of the client's folders when it disconnects.
func (s *watchSet) clear() { s.set(nil) }
