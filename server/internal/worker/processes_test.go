package worker

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func statLine(pid int, name, state string, ppid int, utime, stime uint64, nice, threads int, start, rss uint64) string {
	// pid (comm) state ppid pgrp session tty tpgid flags minflt cminflt
	// majflt cmajflt utime stime cutime cstime priority nice num_threads
	// itrealvalue starttime vsize rss ...
	return fmt.Sprintf("%d (%s) %s %d 1 1 0 -1 0 0 0 0 0 %d %d 0 0 20 %d %d 0 %d 1000 %d 18446744073709551615\n",
		pid, name, state, ppid, utime, stime, nice, threads, start, rss)
}

func TestParseStat(t *testing.T) {
	st, err := parseStat([]byte(statLine(42, "Web Content (2)) x", "S", 7, 150, 50, -5, 12, 9000, 300)))
	if err != nil {
		t.Fatal(err)
	}
	want := procStat{name: "Web Content (2)) x", state: "S", ppid: 7, ticks: 200, nice: -5, threads: 12, start: 9000, rss: 300}
	if st != want {
		t.Fatalf("got %+v\nwant %+v", st, want)
	}
	for _, bad := range []string{"", "42 no parens", "42 (x) S 1 2"} {
		if _, err := parseStat([]byte(bad)); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

// fakeProc writes a /proc with the given stat lines.
func fakeProc(t *testing.T, root string, stats map[int]string, cmdlines map[int]string) {
	t.Helper()
	for pid, stat := range stats {
		dir := filepath.Join(root, strconv.Itoa(pid))
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644)
		os.WriteFile(filepath.Join(dir, "cmdline"), []byte(cmdlines[pid]), 0o644)
	}
}

func TestProcessSamplerCPU(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "self"), 0o755) // not a PID
	fakeProc(t, root, map[int]string{
		2:   statLine(2, "kthreadd", "S", 0, 0, 0, 0, 1, 1, 0),
		10:  statLine(10, "busy", "R", 1, 100, 0, 0, 1, 500, 10),
		11:  statLine(11, "idle", "S", 1, 30, 0, 0, 1, 600, 10),
		300: statLine(300, "kworker/0:1", "I", 2, 0, 0, 0, 1, 700, 0),
	}, map[int]string{10: "busy\x00--hard\x00", 11: "idle\x00"})
	s := &processSampler{}
	t0 := time.Unix(1000, 0)
	first, err := s.list(root, t0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 4 || first[1].PID != 10 || first[1].CPU != 0 {
		t.Fatalf("first sample %+v", first)
	}
	if first[1].Command != "busy --hard" || first[1].Memory != 10*uint64(os.Getpagesize()) || first[1].UID != os.Getuid() {
		t.Fatalf("busy %+v", first[1])
	}
	if !first[0].Kernel || !first[3].Kernel || first[1].Kernel {
		t.Fatalf("kernel threads %+v", first)
	}

	// Two seconds later: busy used 3 s of CPU (150% of one CPU, 75% of
	// two); pid 11 is now another process (another start time).
	fakeProc(t, root, map[int]string{
		10: statLine(10, "busy", "R", 1, 400, 0, 0, 1, 500, 10),
		11: statLine(11, "new", "S", 1, 90, 0, 0, 1, 650, 10),
	}, nil)
	second, _ := s.list(root, t0.Add(2*time.Second), 2)
	cpu := map[int]float64{}
	for _, p := range second {
		cpu[p.PID] = p.CPU
	}
	if cpu[10] != 75 || cpu[11] != 0 {
		t.Fatalf("cpu %v", cpu)
	}

	// A list a moment later (another browser) measures against the same
	// sample, not a few milliseconds.
	third, _ := s.list(root, t0.Add(2100*time.Millisecond), 2)
	for _, p := range third {
		if p.PID == 10 && p.CPU != 0 {
			t.Fatalf("busy with no ticks since: %v", p.CPU)
		}
	}
}

func TestProcessListAndSignals(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Skip("no sleep:", err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	w := driveWorker(t, nil)

	find := func() *Process {
		rec := serve(t, w, "GET", "/api/v1/processes", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("list: %d %s", rec.Code, rec.Body)
		}
		var res struct {
			Processes []Process
			UID, Self int
		}
		json.Unmarshal(rec.Body.Bytes(), &res)
		if res.UID != os.Getuid() || res.Self != os.Getpid() {
			t.Fatalf("uid %d self %d", res.UID, res.Self)
		}
		for i := range res.Processes {
			if res.Processes[i].PID == cmd.Process.Pid {
				return &res.Processes[i]
			}
		}
		return nil
	}
	p := find()
	if p == nil || p.Name != "sleep" || p.Command != "sleep 60" || p.UID != os.Getuid() || p.User == "" || p.Start == 0 {
		t.Fatalf("sleep = %+v", p)
	}
	signal := func(sig string, pid int, start uint64) int {
		body := fmt.Sprintf(`{"pid":%d,"start":%d,"signal":%q}`, pid, start, sig)
		return serve(t, w, "POST", "/api/v1/processes/signal", body).Code
	}
	if code := signal("SEGV", p.PID, p.Start); code != http.StatusBadRequest {
		t.Fatalf("unknown signal: %d", code)
	}
	// Another process now has that PID: nothing is sent.
	if code := signal("TERM", p.PID, p.Start+1); code != http.StatusNotFound {
		t.Fatalf("other start: %d", code)
	}
	if code := signal("STOP", p.PID, p.Start); code != http.StatusNoContent {
		t.Fatalf("stop: %d", code)
	}
	deadline := time.Now().Add(5 * time.Second)
	for find().State != "T" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if st := find().State; st != "T" {
		t.Fatalf("state after STOP: %s", st)
	}
	signal("CONT", p.PID, p.Start)
	if code := signal("TERM", p.PID, p.Start); code != http.StatusNoContent {
		t.Fatalf("term: %d", code)
	}
	err := cmd.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGTERM {
		t.Fatalf("sleep ended with %v", err)
	}
	if code := signal("TERM", p.PID, p.Start); code != http.StatusNotFound {
		t.Fatalf("ended: %d", code)
	}

	// Another user's process: the kernel refuses.
	if os.Geteuid() != 0 {
		data, _ := os.ReadFile("/proc/1/stat")
		st, err := parseStat(data)
		if err == nil {
			if code := signal("TERM", 1, st.start); code != http.StatusForbidden {
				t.Fatalf("pid 1: %d", code)
			}
		}
	}
}
