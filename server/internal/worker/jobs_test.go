package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func startJob(t *testing.T, c *http.Client, op string, sources []string, dest string) JobInfo {
	t.Helper()
	return decode[JobInfo](t, postJSON(t, c, "/api/v1/jobs", map[string]any{"op": op, "sources": sources, "dest": dest}), http.StatusAccepted)
}

// waitJob polls the job until cond holds.
func waitJob(t *testing.T, c *http.Client, id string, cond func(JobInfo) bool) JobInfo {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := c.Get(api("/api/v1/jobs/"+id, nil))
		if err != nil {
			t.Fatal(err)
		}
		j := decode[JobInfo](t, resp, http.StatusOK)
		if cond(j) {
			return j
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s never got there: %+v", id, j)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func finished(j JobInfo) bool { return j.Finished != nil }
func waiting(j JobInfo) bool  { return j.State == JobWaiting }

func runJob(t *testing.T, c *http.Client, op string, sources []string, dest string) JobInfo {
	t.Helper()
	return waitJob(t, c, startJob(t, c, op, sources, dest).ID, finished)
}

func resolve(t *testing.T, c *http.Client, j JobInfo, action, name string, all bool, want int) {
	t.Helper()
	resp := postJSON(t, c, "/api/v1/jobs/"+j.ID+"/resolve", Resolution{Conflict: j.Conflict.ID, Action: action, Name: name, ApplyToAll: all})
	resp.Body.Close()
	if resp.StatusCode != want {
		t.Fatalf("resolve %s: status %d, want %d", action, resp.StatusCode, want)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// noTemps fails if a job left temporary files behind.
func noTemps(t *testing.T, root string) {
	t.Helper()
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && strings.HasPrefix(d.Name(), ".tilde-") {
			t.Errorf("temporary file left behind: %s", p)
		}
		return nil
	})
}

func TestJobCopyTree(t *testing.T) {
	c, home := startWorker(t)
	src := filepath.Join(home, "src")
	write(t, filepath.Join(src, "a.txt"), "alpha")
	write(t, filepath.Join(src, "sub", "b.bin"), strings.Repeat("b", 100_000))
	os.Mkdir(filepath.Join(src, "sub", "empty"), 0o755)
	os.Symlink("a.txt", filepath.Join(src, "link"))
	os.Chmod(filepath.Join(src, "a.txt"), 0o640)
	mtime := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	os.Chtimes(filepath.Join(src, "a.txt"), mtime, mtime)
	dest := filepath.Join(home, "dest")
	os.Mkdir(dest, 0o755)

	j := runJob(t, c, OpCopy, []string{src}, dest)
	if j.State != JobDone || j.ErrorCount != 0 {
		t.Fatalf("job = %+v", j)
	}
	if j.TotalBytes != 100_005 || j.DoneBytes != j.TotalBytes || j.DoneItems != j.TotalItems || j.TotalItems != 6 {
		t.Fatalf("progress = %d/%d bytes, %d/%d items", j.DoneBytes, j.TotalBytes, j.DoneItems, j.TotalItems)
	}
	out := filepath.Join(dest, "src")
	if read(t, filepath.Join(out, "a.txt")) != "alpha" || len(read(t, filepath.Join(out, "sub", "b.bin"))) != 100_000 {
		t.Fatal("contents differ")
	}
	st, _ := os.Stat(filepath.Join(out, "a.txt"))
	if st.Mode().Perm() != 0o640 || !st.ModTime().Equal(mtime) {
		t.Fatalf("a.txt mode %v mtime %v", st.Mode(), st.ModTime())
	}
	if target, err := os.Readlink(filepath.Join(out, "link")); err != nil || target != "a.txt" {
		t.Fatalf("link = %q, %v (links are copied as links)", target, err)
	}
	if st, err := os.Stat(filepath.Join(out, "sub", "empty")); err != nil || !st.IsDir() {
		t.Fatal("empty folder not copied")
	}
	if !exists(filepath.Join(src, "a.txt")) {
		t.Fatal("copy removed the source")
	}
	noTemps(t, home)
}

func TestJobCopyIntoSameFolder(t *testing.T) {
	c, home := startWorker(t)
	write(t, filepath.Join(home, "a.txt"), "x")
	os.Mkdir(filepath.Join(home, "d.v1"), 0o755)
	runJob(t, c, OpCopy, []string{filepath.Join(home, "a.txt"), filepath.Join(home, "d.v1")}, home)
	runJob(t, c, OpCopy, []string{filepath.Join(home, "a.txt")}, home)
	for _, n := range []string{"a (copy).txt", "a (copy 2).txt", "d.v1 (copy)"} {
		if !exists(filepath.Join(home, n)) {
			t.Errorf("%s missing", n)
		}
	}
	// Duplicating a duplicate does not stack suffixes.
	runJob(t, c, OpCopy, []string{filepath.Join(home, "a (copy).txt")}, home)
	if !exists(filepath.Join(home, "a (copy 3).txt")) {
		t.Error("a (copy 3).txt missing")
	}
}

func TestJobConflictReplaceAndMerge(t *testing.T) {
	c, home := startWorker(t)
	src, dest := filepath.Join(home, "src"), filepath.Join(home, "dest")
	write(t, filepath.Join(src, "f1.txt"), "new1")
	write(t, filepath.Join(src, "f2.txt"), "new2")
	write(t, filepath.Join(src, "d", "inner.txt"), "newinner")
	write(t, filepath.Join(dest, "f1.txt"), "old1")
	write(t, filepath.Join(dest, "f2.txt"), "old2")
	write(t, filepath.Join(dest, "d", "inner.txt"), "oldinner")
	write(t, filepath.Join(dest, "d", "keep.txt"), "keep")

	j := startJob(t, c, OpCopy, []string{filepath.Join(src, "f1.txt"), filepath.Join(src, "f2.txt"), filepath.Join(src, "d")}, dest)
	j = waitJob(t, c, j.ID, waiting)
	if j.Conflict.Dest.Path != filepath.Join(dest, "f1.txt") || strings.Join(j.Conflict.Actions, ",") != "replace,skip,rename" {
		t.Fatalf("first conflict = %+v", j.Conflict)
	}
	resolve(t, c, j, ActMerge, "", false, http.StatusBadRequest) // files do not merge
	resolve(t, c, j, ActReplace, "", true, http.StatusNoContent)

	// f2 is replaced without asking; the folder asks.
	j = waitJob(t, c, j.ID, func(j JobInfo) bool { return waiting(j) && j.Conflict.ID == 2 })
	if j.Conflict.Source.Type != "dir" || strings.Join(j.Conflict.Actions, ",") != "merge,skip,rename" {
		t.Fatalf("second conflict = %+v", j.Conflict)
	}
	stale := j
	stale.Conflict = &Conflict{ID: 1}
	resolve(t, c, stale, ActReplace, "", false, http.StatusConflict)
	resolve(t, c, j, ActMerge, "", false, http.StatusNoContent)

	// inner.txt is a file conflict: "replace all" still applies.
	j = waitJob(t, c, j.ID, finished)
	if j.State != JobDone || j.ErrorCount != 0 {
		t.Fatalf("job = %+v", j)
	}
	for p, want := range map[string]string{"f1.txt": "new1", "f2.txt": "new2", "d/inner.txt": "newinner", "d/keep.txt": "keep"} {
		if got := read(t, filepath.Join(dest, p)); got != want {
			t.Errorf("%s = %q, want %q", p, got, want)
		}
	}
	noTemps(t, home)
}

func TestJobConflictRenameAndSkip(t *testing.T) {
	c, home := startWorker(t)
	src, dest := filepath.Join(home, "src"), filepath.Join(home, "dest")
	write(t, filepath.Join(src, "a.txt"), "new a")
	write(t, filepath.Join(src, "b.txt"), "new b")
	write(t, filepath.Join(dest, "a.txt"), "old a")
	write(t, filepath.Join(dest, "b.txt"), "old b")

	j := startJob(t, c, OpMove, []string{filepath.Join(src, "a.txt"), filepath.Join(src, "b.txt")}, dest)
	j = waitJob(t, c, j.ID, waiting)
	if j.Conflict.Suggested != "a (2).txt" {
		t.Fatalf("suggested = %q", j.Conflict.Suggested)
	}
	resolve(t, c, j, ActRename, "../escape.txt", false, http.StatusBadRequest)
	resolve(t, c, j, ActRename, "a renamed.txt", false, http.StatusNoContent)
	j = waitJob(t, c, j.ID, func(j JobInfo) bool { return waiting(j) && j.Conflict.ID == 2 })
	resolve(t, c, j, ActSkip, "", false, http.StatusNoContent)
	j = waitJob(t, c, j.ID, finished)

	if read(t, filepath.Join(dest, "a renamed.txt")) != "new a" || read(t, filepath.Join(dest, "a.txt")) != "old a" {
		t.Error("rename went wrong")
	}
	if read(t, filepath.Join(dest, "b.txt")) != "old b" || !exists(filepath.Join(src, "b.txt")) {
		t.Error("skip went wrong")
	}
	if exists(filepath.Join(src, "a.txt")) {
		t.Error("moved file still in the source")
	}
}

func TestJobMove(t *testing.T) {
	c, home := startWorker(t)
	write(t, filepath.Join(home, "d", "x.txt"), "x")
	write(t, filepath.Join(home, "f.txt"), "f")
	dest := filepath.Join(home, "dest")
	os.Mkdir(dest, 0o755)

	j := runJob(t, c, OpMove, []string{filepath.Join(home, "d"), filepath.Join(home, "f.txt")}, dest)
	if j.State != JobDone || j.ErrorCount != 0 {
		t.Fatalf("job = %+v", j)
	}
	if exists(filepath.Join(home, "d")) || read(t, filepath.Join(dest, "d", "x.txt")) != "x" || read(t, filepath.Join(dest, "f.txt")) != "f" {
		t.Fatal("move went wrong")
	}

	// A folder cannot go into itself; moving onto its own folder is a no-op.
	j = runJob(t, c, OpMove, []string{filepath.Join(dest, "d"), filepath.Join(dest, "f.txt")}, filepath.Join(dest, "d"))
	if j.ErrorCount != 1 || j.Errors[0].Path != filepath.Join(dest, "d") {
		t.Fatalf("errors = %+v", j.Errors)
	}
	if !exists(filepath.Join(dest, "d", "f.txt")) {
		t.Fatal("f.txt was not moved")
	}
	j = runJob(t, c, OpMove, []string{filepath.Join(dest, "d", "f.txt")}, filepath.Join(dest, "d"))
	if j.State != JobDone || j.ErrorCount != 0 || !exists(filepath.Join(dest, "d", "f.txt")) {
		t.Fatalf("no-op move = %+v", j)
	}
}

// TestJobAcrossFilesystems needs a second filesystem; /dev/shm is a tmpfs on
// most Linux systems.
func TestJobAcrossFilesystems(t *testing.T) {
	c, home := startWorker(t)
	other, err := os.MkdirTemp("/dev/shm", "tilde-test-")
	if err != nil {
		t.Skip("no /dev/shm")
	}
	defer os.RemoveAll(other)
	var a, b syscall.Stat_t
	syscall.Stat(home, &a)
	syscall.Stat(other, &b)
	if a.Dev == b.Dev {
		t.Skip("/dev/shm is on the same filesystem")
	}
	write(t, filepath.Join(other, "moved", "m.txt"), "m")
	write(t, filepath.Join(other, "dropped.txt"), "d")

	j := runJob(t, c, OpMove, []string{filepath.Join(other, "moved")}, home)
	if j.State != JobDone || j.ErrorCount != 0 || read(t, filepath.Join(home, "moved", "m.txt")) != "m" || exists(filepath.Join(other, "moved")) {
		t.Fatalf("move across filesystems: %+v", j)
	}
	// Drag and drop copies across filesystems, like Nemo.
	j = runJob(t, c, OpDrop, []string{filepath.Join(other, "dropped.txt")}, home)
	if j.State != JobDone || read(t, filepath.Join(home, "dropped.txt")) != "d" || !exists(filepath.Join(other, "dropped.txt")) {
		t.Fatalf("drop across filesystems: %+v", j)
	}
	noTemps(t, home)
}

func TestJobDelete(t *testing.T) {
	c, home := startWorker(t)
	write(t, filepath.Join(home, "tree", "a", "b.txt"), "b")
	os.Symlink("/etc", filepath.Join(home, "tree", "link"))
	j := runJob(t, c, OpDelete, []string{filepath.Join(home, "tree")}, "")
	if j.State != JobDone || j.ErrorCount != 0 || j.DoneItems != 4 || exists(filepath.Join(home, "tree")) {
		t.Fatalf("delete = %+v", j)
	}
	if !exists("/etc/hostname") && !exists("/etc/passwd") {
		t.Fatal("delete followed the link")
	}

	// Deleting from the trash removes the .trashinfo too.
	write(t, filepath.Join(home, "gone.txt"), "g")
	resp := postJSON(t, c, "/api/v1/fs/trash", map[string][]string{"paths": {filepath.Join(home, "gone.txt")}})
	name := decode[struct{ Results []struct{ Name string } }](t, resp, http.StatusOK).Results[0].Name
	trash := filepath.Join(home, ".local", "share", "Trash")
	runJob(t, c, OpDelete, []string{filepath.Join(trash, "files", name)}, "")
	if exists(filepath.Join(trash, "files", name)) || exists(filepath.Join(trash, "info", name+".trashinfo")) {
		t.Fatal("trash item or its info survived")
	}
}

func TestJobCancel(t *testing.T) {
	c, home := startWorker(t)
	big := filepath.Join(home, "big")
	f, _ := os.Create(big)
	f.Truncate(3 * copyChunk)
	f.Close()
	dest := filepath.Join(home, "dest")
	os.Mkdir(dest, 0o755)

	testHookCopyChunk = func(j *job) { j.cancel() }
	defer func() { testHookCopyChunk = nil }()
	j := runJob(t, c, OpCopy, []string{big}, dest)
	if j.State != JobCancelled || exists(filepath.Join(dest, "big")) {
		t.Fatalf("cancelled copy = %+v", j)
	}
	noTemps(t, home)
	testHookCopyChunk = nil

	// Cancel while a conflict waits for an answer.
	write(t, filepath.Join(dest, "big"), "old")
	j = startJob(t, c, OpCopy, []string{big}, dest)
	waitJob(t, c, j.ID, waiting)
	postJSON(t, c, "/api/v1/jobs/"+j.ID+"/cancel", nil).Body.Close()
	j = waitJob(t, c, j.ID, finished)
	if j.State != JobCancelled || read(t, filepath.Join(dest, "big")) != "old" {
		t.Fatalf("cancelled at conflict = %+v", j)
	}

	// Finished jobs can be dismissed.
	req, _ := http.NewRequest(http.MethodDelete, api("/api/v1/jobs/"+j.ID, nil), nil)
	resp, _ := c.Do(req)
	resp.Body.Close()
	resp, _ = c.Get(api("/api/v1/jobs/"+j.ID, nil))
	decode[apiError](t, resp, http.StatusNotFound)
}

func TestJobPermissionErrors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	c, home := startWorker(t)
	src := filepath.Join(home, "src")
	write(t, filepath.Join(src, "ok.txt"), "ok")
	write(t, filepath.Join(src, "secret.txt"), "s")
	os.Chmod(filepath.Join(src, "secret.txt"), 0)
	dest := filepath.Join(home, "dest")
	os.Mkdir(dest, 0o755)

	j := runJob(t, c, OpCopy, []string{src}, dest)
	if j.State != JobDone || j.ErrorCount != 1 || j.Errors[0].Code != "permission_denied" {
		t.Fatalf("job = %+v", j)
	}
	if read(t, filepath.Join(dest, "src", "ok.txt")) != "ok" || exists(filepath.Join(dest, "src", "secret.txt")) {
		t.Fatal("other files should still be copied")
	}
	noTemps(t, home)
}

func TestJobBadRequests(t *testing.T) {
	c, home := startWorker(t)
	for _, body := range []map[string]any{
		{"op": "shred", "sources": []string{home}},
		{"op": "copy", "sources": []string{}, "dest": home},
		{"op": "copy", "sources": []string{"relative"}, "dest": home},
		{"op": "move", "sources": []string{home + "/x"}},
	} {
		decode[apiError](t, postJSON(t, c, "/api/v1/jobs", body), http.StatusBadRequest)
	}
	// A destination that is not a folder fails the whole job.
	write(t, filepath.Join(home, "file"), "x")
	j := runJob(t, c, OpCopy, []string{filepath.Join(home, "file")}, filepath.Join(home, "file"))
	if j.State != JobFailed || j.Error == "" {
		t.Fatalf("job = %+v", j)
	}
}

func TestEventsStream(t *testing.T) {
	c, home := startWorker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://worker/api/v1/events", &websocket.DialOptions{HTTPClient: c})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	next := func() event {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var ev event
		if err := json.Unmarshal(data, &ev); err != nil {
			t.Fatal(err)
		}
		return ev
	}
	if ev := next(); ev.Type != "snapshot" || len(ev.Jobs) != 0 {
		t.Fatalf("first event = %+v", ev)
	}

	write(t, filepath.Join(home, "a.txt"), "a")
	j := startJob(t, c, OpCopy, []string{filepath.Join(home, "a.txt")}, home)
	for {
		ev := next()
		if ev.Type == "job" && ev.Job.ID == j.ID && ev.Job.State == JobDone {
			break
		}
	}
	req, _ := http.NewRequest(http.MethodDelete, api("/api/v1/jobs/"+j.ID, nil), nil)
	resp, _ := c.Do(req)
	resp.Body.Close()
	if ev := next(); ev.Type != "job-removed" || ev.ID != j.ID {
		t.Fatalf("after dismiss: %+v", ev)
	}
}

func TestFreeName(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.txt"), "")
	write(t, filepath.Join(dir, "a (2).txt"), "")
	tests := []struct {
		name      string
		isDir     bool
		duplicate bool
		want      string
	}{
		{"a.txt", false, false, "a (3).txt"},
		{"a.txt", false, true, "a (copy).txt"},
		{"a (copy).txt", false, true, "a (copy).txt"},
		{"x.tar.gz", false, false, "x (2).tar.gz"},
		{".bashrc", false, false, ".bashrc (2)"},
		{"my.folder", true, true, "my.folder (copy)"},
	}
	for _, tt := range tests {
		if got := freeName(dir, tt.name, tt.isDir, tt.duplicate); got != tt.want {
			t.Errorf("freeName(%q, dir=%v, dup=%v) = %q, want %q", tt.name, tt.isDir, tt.duplicate, got, tt.want)
		}
	}
}
