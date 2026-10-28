package worker

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The process manager's view of /proc (D59): every process the kernel lets
// the user see (all of them, unless /proc is mounted with hidepid), read
// as the user. Signals go out as the user too, so the kernel decides whom
// they may reach.

// Process is one row of the System Monitor's process list.
type Process struct {
	PID  int `json:"pid"`
	PPID int `json:"ppid"`
	// Name is the kernel's name for it (comm, 15 bytes at most); Command
	// its command line, empty for kernel threads and when unreadable.
	Name    string `json:"name"`
	Command string `json:"command,omitempty"`
	UID     int    `json:"uid"`
	User    string `json:"user"`
	// State is the kernel's letter: R running, S sleeping, D waiting on
	// I/O, Z zombie, T stopped, I idle, ...
	State string `json:"state"`
	// CPU is the share of all CPUs since the last sample, in percent.
	CPU float64 `json:"cpu"`
	// Memory is the resident set, in bytes.
	Memory  uint64 `json:"memory"`
	Nice    int    `json:"nice"`
	Threads int    `json:"threads"`
	// Start is when it started, in clock ticks after boot: with the PID it
	// names this process and not a later one that got the same PID.
	Start uint64 `json:"start"`
	// Kernel marks kernel threads.
	Kernel bool `json:"kernel,omitempty"`
}

// clockTicks is USER_HZ, the unit of /proc's times: 100 on every
// architecture Linux runs desktops on.
const clockTicks = 100

type procSample struct {
	ticks uint64 // utime + stime
	start uint64
}

// processSampler keeps the last sample, for CPU shares between two lists.
type processSampler struct {
	mu    sync.Mutex
	last  map[int]procSample
	at    time.Time
	users map[int]string
}

// procStat is what /proc/<pid>/stat says.
type procStat struct {
	name    string
	state   string
	ppid    int
	ticks   uint64
	nice    int
	threads int
	start   uint64
	rss     uint64 // pages
}

// parseStat reads /proc/<pid>/stat. The name is in parentheses and may
// hold spaces and parentheses itself, so the fields start after the last
// ")".
func parseStat(data []byte) (procStat, error) {
	open := bytes.IndexByte(data, '(')
	end := bytes.LastIndexByte(data, ')')
	if open < 0 || end < open {
		return procStat{}, errors.New("malformed stat")
	}
	f := strings.Fields(string(data[end+1:]))
	// Field 3 of proc(5) is f[0]: state, ppid, ..., utime (14), stime (15),
	// nice (19), num_threads (20), starttime (22), rss (24).
	if len(f) < 22 {
		return procStat{}, errors.New("short stat")
	}
	num := func(i int) uint64 { n, _ := strconv.ParseUint(f[i], 10, 64); return n }
	ppid, _ := strconv.Atoi(f[1])
	nice, _ := strconv.Atoi(f[16])
	threads, _ := strconv.Atoi(f[17])
	return procStat{
		name:    string(data[open+1 : end]),
		state:   f[0],
		ppid:    ppid,
		ticks:   num(11) + num(12),
		nice:    nice,
		threads: threads,
		start:   num(19),
		rss:     num(21),
	}, nil
}

func (s *processSampler) userName(uid int) string {
	if s.users == nil {
		s.users = map[int]string{}
	}
	if name, ok := s.users[uid]; ok {
		return name
	}
	name := strconv.Itoa(uid)
	if u, err := user.LookupId(name); err == nil {
		name = u.Username
	}
	s.users[uid] = name
	return name
}

// list reads every process under root (/proc) and works out CPU shares
// against the previous sample. Two lists less than a second apart (two
// browsers) share that sample rather than measure a sliver of time.
func (s *processSampler) list(root string, now time.Time, cpus int) ([]Process, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	elapsed := now.Sub(s.at).Seconds()
	next := make(map[int]procSample, len(entries))
	page := uint64(os.Getpagesize())
	out := make([]Process, 0, len(entries))
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		dir := filepath.Join(root, e.Name())
		data, err := os.ReadFile(filepath.Join(dir, "stat"))
		if err != nil {
			continue // gone meanwhile
		}
		st, err := parseStat(data)
		if err != nil {
			continue
		}
		p := Process{
			PID: pid, PPID: st.ppid, Name: st.name, State: st.state,
			Memory: st.rss * page, Nice: st.nice, Threads: st.threads, Start: st.start,
		}
		// The directory belongs to the process's effective user.
		if fi, err := os.Stat(dir); err == nil {
			if sys, ok := fi.Sys().(*syscall.Stat_t); ok {
				p.UID = int(sys.Uid)
			}
		}
		p.User = s.userName(p.UID)
		if cmd, err := os.ReadFile(filepath.Join(dir, "cmdline")); err == nil {
			p.Command = strings.TrimSpace(strings.ReplaceAll(string(cmd), "\x00", " "))
		}
		p.Kernel = p.Command == "" && (pid == 2 || st.ppid == 2)
		if prev, ok := s.last[pid]; ok && prev.start == st.start && elapsed > 0 && st.ticks >= prev.ticks {
			p.CPU = float64(st.ticks-prev.ticks) / clockTicks / elapsed / float64(max(1, cpus)) * 100
			p.CPU = float64(int(p.CPU*10+0.5)) / 10
		}
		next[pid] = procSample{ticks: st.ticks, start: st.start}
		out = append(out, p)
	}
	if s.last == nil || elapsed >= 1 {
		s.last, s.at = next, now
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out, nil
}

// procRoot is where /proc is; tests point it elsewhere.
var procRoot = "/proc"

func (w *Worker) handleProcesses(rw http.ResponseWriter, r *http.Request) {
	procs, err := w.procs.list(procRoot, time.Now(), runtime.NumCPU())
	if err != nil {
		writeErr(rw, err)
		return
	}
	writeJSON(rw, http.StatusOK, map[string]any{
		"processes": procs,
		"uid":       os.Getuid(),
		// The worker itself: ending it ends the session.
		"self": os.Getpid(),
		"cpus": runtime.NumCPU(),
	})
}

// The signals the System Monitor sends: End, Kill, Stop and Continue, as
// GNOME's, and Hang Up and Interrupt for shells and servers.
var processSignals = map[string]syscall.Signal{
	"TERM": syscall.SIGTERM,
	"KILL": syscall.SIGKILL,
	"STOP": syscall.SIGSTOP,
	"CONT": syscall.SIGCONT,
	"HUP":  syscall.SIGHUP,
	"INT":  syscall.SIGINT,
}

// errProcessGone: the PID is free, or names a later process now.
var errProcessGone = errors.New("the process has ended")

// handleProcessSignal sends a signal to one process, named by its PID and
// start time: between the list and the click the process may have ended
// and its PID gone to another.
func (w *Worker) handleProcessSignal(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		PID    int    `json:"pid"`
		Start  uint64 `json:"start"`
		Signal string `json:"signal"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(rw, err)
		return
	}
	sig, ok := processSignals[req.Signal]
	if !ok || req.PID <= 0 {
		writeErr(rw, badRequest{"unknown signal or process"})
		return
	}
	data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(req.PID), "stat"))
	var st procStat
	if err == nil {
		st, err = parseStat(data)
	}
	if err != nil || st.start != req.Start {
		writeJSON(rw, http.StatusNotFound, apiError{Error: errProcessGone.Error(), Code: "gone"})
		return
	}
	if err := syscall.Kill(req.PID, sig); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			writeJSON(rw, http.StatusNotFound, apiError{Error: errProcessGone.Error(), Code: "gone"})
			return
		}
		writeErr(rw, fmt.Errorf("signal %d: %w", req.PID, err))
		return
	}
	rw.WriteHeader(http.StatusNoContent)
}
