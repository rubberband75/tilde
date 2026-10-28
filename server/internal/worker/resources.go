package worker

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The system monitor applet's and Resources tab's figures (D61): CPU,
// memory, swap, network and disk, from /proc. The worker samples once a
// second only while some browser watches (an events-socket message), and
// sends each watcher the whole snapshot, never a change against an earlier
// one: a browser that missed one is never out of step. The last minute is
// kept, so a graph opened later starts full.

// Resources is one snapshot.
type Resources struct {
	// Time is when it was taken, in Unix milliseconds.
	Time int64 `json:"time"`
	// CPU is the share of all CPUs busy since the last snapshot, Cores the
	// same per CPU, in percent.
	CPU   float64   `json:"cpu"`
	Cores []float64 `json:"cores"`
	// Memory: total and used (total minus available) and cache, in bytes.
	Memory MemoryUse `json:"memory"`
	Swap   MemoryUse `json:"swap"`
	// Network and disk: bytes a second since the last snapshot, and network
	// totals since boot, over every interface but loopback and every disk
	// but loop, RAM, zram and device-mapper ones (counted in their disks).
	NetReceive float64 `json:"netReceive"`
	NetSend    float64 `json:"netSend"`
	NetRecvAll uint64  `json:"netReceived"`
	NetSentAll uint64  `json:"netSent"`
	DiskRead   float64 `json:"diskRead"`
	DiskWrite  float64 `json:"diskWrite"`
}

type MemoryUse struct {
	Total uint64 `json:"total"`
	Used  uint64 `json:"used"`
	Cache uint64 `json:"cache,omitempty"`
}

// cpuTimes is one cpu line of /proc/stat: busy and all ticks.
type cpuTimes struct{ busy, total uint64 }

// counters is what the rates are worked out from.
type counters struct {
	at                 time.Time
	cpu                cpuTimes
	cores              []cpuTimes
	rx, tx             uint64
	sectorsR, sectorsW uint64
}

func parseCPU(fields []string) cpuTimes {
	var t cpuTimes
	for i, f := range fields {
		n, _ := strconv.ParseUint(f, 10, 64)
		// user nice system idle iowait irq softirq steal; guest time is
		// already in user.
		if i >= 8 {
			break
		}
		t.total += n
		if i != 3 && i != 4 {
			t.busy += n
		}
	}
	return t
}

// parseProcStat reads the cpu lines of /proc/stat.
func parseProcStat(data []byte) (cpuTimes, []cpuTimes) {
	var all cpuTimes
	var cores []cpuTimes
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 5 || !strings.HasPrefix(f[0], "cpu") {
			continue
		}
		if f[0] == "cpu" {
			all = parseCPU(f[1:])
		} else {
			cores = append(cores, parseCPU(f[1:]))
		}
	}
	return all, cores
}

// parseMeminfo reads /proc/meminfo (kB) into memory and swap use.
func parseMeminfo(data []byte) (MemoryUse, MemoryUse) {
	v := map[string]uint64{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) > 0 {
			n, _ := strconv.ParseUint(f[0], 10, 64)
			v[name] = n * 1024
		}
	}
	mem := MemoryUse{Total: v["MemTotal"], Cache: v["Cached"] + v["Buffers"]}
	if avail, ok := v["MemAvailable"]; ok && avail <= mem.Total {
		mem.Used = mem.Total - avail
	}
	swap := MemoryUse{Total: v["SwapTotal"]}
	if v["SwapFree"] <= swap.Total {
		swap.Used = swap.Total - v["SwapFree"]
	}
	return mem, swap
}

// parseNetDev sums received and sent bytes over every interface but lo.
func parseNetDev(data []byte) (rx, tx uint64) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok || strings.TrimSpace(name) == "lo" {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		r, _ := strconv.ParseUint(f[0], 10, 64)
		t, _ := strconv.ParseUint(f[8], 10, 64)
		rx += r
		tx += t
	}
	return rx, tx
}

// countedDisk: whole disks only. Partitions are in their disk's figures,
// and loop, RAM, zram, device-mapper and md devices in the disks under them.
func countedDisk(name string, isDisk func(string) bool) bool {
	for _, p := range []string{"loop", "ram", "zram", "dm-", "md", "sr"} {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	return isDisk(name)
}

// parseDiskstats sums sectors read and written over whole disks.
func parseDiskstats(data []byte, isDisk func(string) bool) (read, written uint64) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		// major minor name reads merged sectors_read ms writes merged sectors_written ...
		if len(f) < 10 || !countedDisk(f[2], isDisk) {
			continue
		}
		r, _ := strconv.ParseUint(f[5], 10, 64)
		w, _ := strconv.ParseUint(f[9], 10, 64)
		read += r
		written += w
	}
	return read, written
}

// procFiles reads what a snapshot needs; tests replace it.
type procFiles struct {
	root   string // /proc
	sysBlk string // /sys/block
}

func (p procFiles) read(name string) []byte {
	b, err := os.ReadFile(filepath.Join(p.root, name))
	if err != nil {
		return nil
	}
	return b
}

func (p procFiles) isDisk(name string) bool {
	_, err := os.Stat(filepath.Join(p.sysBlk, name))
	return err == nil
}

// sample takes counters and the memory figures now.
func (p procFiles) sample(now time.Time) (counters, MemoryUse, MemoryUse) {
	c := counters{at: now}
	c.cpu, c.cores = parseProcStat(p.read("stat"))
	c.rx, c.tx = parseNetDev(p.read("net/dev"))
	c.sectorsR, c.sectorsW = parseDiskstats(p.read("diskstats"), p.isDisk)
	mem, swap := parseMeminfo(p.read("meminfo"))
	return c, mem, swap
}

func share(now, before cpuTimes) float64 {
	if now.total <= before.total || now.busy < before.busy {
		return 0
	}
	pct := float64(now.busy-before.busy) / float64(now.total-before.total) * 100
	return float64(int(pct*10+0.5)) / 10
}

func rate(now, before uint64, secs float64) float64 {
	if now < before || secs <= 0 {
		return 0
	}
	return float64(now-before) / secs
}

// snapshotFrom works out the rates between two counters.
func snapshotFrom(now, before counters, mem, swap MemoryUse) Resources {
	secs := now.at.Sub(before.at).Seconds()
	r := Resources{
		Time: now.at.UnixMilli(), CPU: share(now.cpu, before.cpu), Cores: make([]float64, len(now.cores)),
		Memory: mem, Swap: swap, NetRecvAll: now.rx, NetSentAll: now.tx,
		NetReceive: rate(now.rx, before.rx, secs), NetSend: rate(now.tx, before.tx, secs),
		DiskRead: rate(now.sectorsR, before.sectorsR, secs) * 512, DiskWrite: rate(now.sectorsW, before.sectorsW, secs) * 512,
	}
	for i := range now.cores {
		if i < len(before.cores) {
			r.Cores[i] = share(now.cores[i], before.cores[i])
		}
	}
	return r
}

// resourceHistory is how many snapshots a graph opened later starts with.
const resourceHistory = 60

// resourceMonitor samples while some client watches.
type resourceMonitor struct {
	files    procFiles
	interval time.Duration

	mu       sync.Mutex
	watchers map[chan []byte]struct{}
	history  []Resources
	stop     chan struct{}
}

func newResourceMonitor() *resourceMonitor {
	return &resourceMonitor{
		files:    procFiles{root: "/proc", sysBlk: "/sys/block"},
		interval: time.Second,
		watchers: map[chan []byte]struct{}{},
	}
}

// resourceEvent is a frame for one client: the latest snapshot, or the
// history when it starts watching.
type resourceEvent struct {
	Type      string      `json:"type"` // "resources" or "resources-history"
	Resources *Resources  `json:"resources,omitempty"`
	History   []Resources `json:"history,omitempty"`
}

func encode(ev resourceEvent) []byte {
	b, err := json.Marshal(ev)
	if err != nil {
		slog.Error("worker: encode resources", "err", err)
	}
	return b
}

func send(ch chan []byte, b []byte) {
	select {
	case ch <- b:
	default: // behind: it gets the next whole snapshot
	}
}

// watch makes ch receive snapshots, starting with the history; the first
// watcher starts the sampling.
func (m *resourceMonitor) watch(ch chan []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.watchers[ch]; ok {
		return
	}
	m.watchers[ch] = struct{}{}
	send(ch, encode(resourceEvent{Type: "resources-history", History: append([]Resources{}, m.history...)}))
	if m.stop == nil {
		m.stop = make(chan struct{})
		go m.run(m.stop)
	}
}

// unwatch stops ch's snapshots; the last watcher stops the sampling, and
// the history goes with it.
func (m *resourceMonitor) unwatch(ch chan []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.watchers, ch)
	if len(m.watchers) == 0 && m.stop != nil {
		close(m.stop)
		m.stop = nil
		m.history = nil
	}
}

func (m *resourceMonitor) run(stop chan struct{}) {
	before, _, _ := m.files.sample(time.Now())
	t := time.NewTicker(m.interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		now, mem, swap := m.files.sample(time.Now())
		snap := snapshotFrom(now, before, mem, swap)
		before = now
		b := encode(resourceEvent{Type: "resources", Resources: &snap})
		m.mu.Lock()
		if m.stop != stop {
			m.mu.Unlock()
			return
		}
		m.history = append(m.history, snap)
		if len(m.history) > resourceHistory {
			m.history = m.history[len(m.history)-resourceHistory:]
		}
		for ch := range m.watchers {
			send(ch, b)
		}
		m.mu.Unlock()
	}
}

func (m *resourceMonitor) close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stop != nil {
		close(m.stop)
		m.stop = nil
	}
}
