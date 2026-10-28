package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/coder/websocket"
)

const testStat = `cpu  100 0 100 700 100 0 0 0 0 0
cpu0 50 0 50 350 50 0 0 0 0 0
cpu1 50 0 50 350 50 0 0 0 0 0
intr 12345
ctxt 678
`

const testMeminfo = `MemTotal:        8000000 kB
MemFree:         1000000 kB
MemAvailable:    6000000 kB
Buffers:          100000 kB
Cached:          2000000 kB
SwapTotal:       2000000 kB
SwapFree:        1500000 kB
`

const testNetDev = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 9999999     100    0    0    0     0          0         0  9999999     100    0    0    0     0       0          0
  eth0: 1000000    1000    0    0    0     0          0         0   200000     500    0    0    0     0       0          0
 wlan0:  500000     400    0    0    0     0          0         0   100000     300    0    0    0     0       0          0
`

const testDiskstats = `   8       0 sda 100 0 2000 0 50 0 1000 0 0 0 0 0 0 0 0 0 0
   8       1 sda1 90 0 1800 0 40 0 900 0 0 0 0 0 0 0 0 0 0
   7       0 loop0 10 0 80 0 0 0 0 0 0 0 0 0 0 0 0 0 0
 253       0 dm-0 80 0 1600 0 30 0 800 0 0 0 0 0 0 0 0 0 0
 259       0 nvme0n1 10 0 400 0 5 0 100 0 0 0 0 0 0 0 0 0 0
`

func TestResourceParsers(t *testing.T) {
	all, cores := parseProcStat([]byte(testStat))
	if all != (cpuTimes{busy: 200, total: 1000}) || len(cores) != 2 || cores[1] != (cpuTimes{busy: 100, total: 500}) {
		t.Fatalf("stat %+v %+v", all, cores)
	}
	mem, swap := parseMeminfo([]byte(testMeminfo))
	if mem != (MemoryUse{Total: 8000000 * 1024, Used: 2000000 * 1024, Cache: 2100000 * 1024}) {
		t.Fatalf("memory %+v", mem)
	}
	if swap != (MemoryUse{Total: 2000000 * 1024, Used: 500000 * 1024}) {
		t.Fatalf("swap %+v", swap)
	}
	if rx, tx := parseNetDev([]byte(testNetDev)); rx != 1500000 || tx != 300000 {
		t.Fatalf("net %d %d", rx, tx)
	}
	// Whole disks only: not partitions, loop or device-mapper devices.
	disks := map[string]bool{"sda": true, "loop0": true, "dm-0": true, "nvme0n1": true}
	if r, w := parseDiskstats([]byte(testDiskstats), func(n string) bool { return disks[n] }); r != 2400 || w != 1100 {
		t.Fatalf("disks %d %d", r, w)
	}
}

func TestSnapshotRates(t *testing.T) {
	t0 := time.Unix(100, 0)
	before := counters{at: t0, cpu: cpuTimes{200, 1000}, cores: []cpuTimes{{100, 500}, {100, 500}}, rx: 1000, tx: 500, sectorsR: 10, sectorsW: 0}
	now := counters{at: t0.Add(2 * time.Second), cpu: cpuTimes{500, 1400}, cores: []cpuTimes{{250, 700}, {150, 700}}, rx: 5000, tx: 500, sectorsR: 30, sectorsW: 8}
	r := snapshotFrom(now, before, MemoryUse{Total: 10}, MemoryUse{})
	if r.CPU != 75 || r.Cores[1] != 25 {
		t.Fatalf("cpu %v cores %v", r.CPU, r.Cores)
	}
	if r.NetReceive != 2000 || r.NetSend != 0 || r.NetRecvAll != 5000 {
		t.Fatalf("net %+v", r)
	}
	if r.DiskRead != 10*512 || r.DiskWrite != 4*512 {
		t.Fatalf("disk %v %v", r.DiskRead, r.DiskWrite)
	}
	if r.Time != now.at.UnixMilli() {
		t.Fatalf("time %d", r.Time)
	}
	// A counter that went back (an interface went away) is no rate.
	if r := snapshotFrom(before, now, MemoryUse{}, MemoryUse{}); r.NetReceive != 0 || r.CPU != 0 {
		t.Fatalf("backwards %+v", r)
	}
}

func fakeResourceFiles(t *testing.T) procFiles {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "net"), 0o755)
	for name, data := range map[string]string{
		"stat": testStat, "meminfo": testMeminfo, "net/dev": testNetDev, "diskstats": testDiskstats,
	} {
		os.WriteFile(filepath.Join(root, name), []byte(data), 0o644)
	}
	blk := t.TempDir()
	os.Mkdir(filepath.Join(blk, "sda"), 0o755)
	return procFiles{root: root, sysBlk: blk}
}

func receive(t *testing.T, ch chan []byte) resourceEvent {
	t.Helper()
	select {
	case b := <-ch:
		var ev resourceEvent
		if err := json.Unmarshal(b, &ev); err != nil {
			t.Fatal(err)
		}
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("no resources")
	}
	return resourceEvent{}
}

func TestResourceMonitorSamplesWhileWatched(t *testing.T) {
	m := newResourceMonitor()
	m.files = fakeResourceFiles(t)
	m.interval = 20 * time.Millisecond
	a := make(chan []byte, 4)
	m.watch(a)
	if ev := receive(t, a); ev.Type != "resources-history" || len(ev.History) != 0 {
		t.Fatalf("first %+v", ev)
	}
	ev := receive(t, a)
	if ev.Type != "resources" || ev.Resources.Memory.Total != 8000000*1024 || len(ev.Resources.Cores) != 2 {
		t.Fatalf("snapshot %+v", ev)
	}

	// A later watcher starts with what was sampled so far.
	b := make(chan []byte, 4)
	m.watch(b)
	if ev := receive(t, b); ev.Type != "resources-history" || len(ev.History) == 0 {
		t.Fatalf("later watcher %+v", ev)
	}

	m.unwatch(a)
	m.unwatch(a) // twice is harmless
	m.mu.Lock()
	running := m.stop != nil
	m.mu.Unlock()
	if !running {
		t.Fatal("stopped with a watcher left")
	}
	m.unwatch(b)
	m.mu.Lock()
	stopped, history := m.stop == nil, len(m.history)
	m.mu.Unlock()
	if !stopped || history != 0 {
		t.Fatalf("after the last watcher: stopped %v, history %d", stopped, history)
	}
}

func TestResourcesOverEvents(t *testing.T) {
	c, _ := startWorker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://worker/api/v1/events", &websocket.DialOptions{HTTPClient: c})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	send := func(on bool) {
		b, _ := json.Marshal(clientMessage{Type: "resources", On: on})
		if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
			t.Fatal(err)
		}
	}
	next := func(typ string) resourceEvent {
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				t.Fatalf("waiting for %s: %v", typ, err)
			}
			var ev resourceEvent
			if json.Unmarshal(data, &ev) == nil && ev.Type == typ {
				return ev
			}
		}
	}
	send(true)
	next("resources-history")
	ev := next("resources")
	r := ev.Resources
	if r == nil || len(r.Cores) != runtime.NumCPU() || r.Memory.Total == 0 || r.Memory.Used == 0 || r.Time == 0 {
		t.Fatalf("snapshot from /proc: %+v", r)
	}
	if r.CPU < 0 || r.CPU > 100 {
		t.Fatalf("cpu %v", r.CPU)
	}
}
