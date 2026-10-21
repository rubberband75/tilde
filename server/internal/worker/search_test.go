package worker

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

type searchResult struct {
	names     []string
	total     int
	truncated bool
}

func search(t *testing.T, c *http.Client, q url.Values) searchResult {
	t.Helper()
	resp, err := c.Get(api("/api/v1/fs/search", q))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var res searchResult
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(nil, 8<<20)
	for sc.Scan() {
		var line struct {
			Entries   []Entry
			Done      bool
			Total     int
			Truncated bool
		}
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			t.Fatal(err)
		}
		for _, e := range line.Entries {
			rel, _ := filepath.Rel(q.Get("path"), e.Path)
			res.names = append(res.names, rel)
		}
		if line.Done {
			res.total, res.truncated = line.Total, line.Truncated
		}
	}
	sort.Strings(res.names)
	return res
}

func TestSearch(t *testing.T) {
	c, home := startWorker(t)
	for _, p := range []string{
		"Documents/Tax Report 2025.pdf",
		"Documents/old/tax notes.txt",
		"Pictures/beach.png",
		".config/tax-tool/settings",
		"tax-folder/readme",
	} {
		write(t, filepath.Join(home, p), "x")
	}
	os.Symlink(filepath.Join(home, "Documents"), filepath.Join(home, "link-to-docs"))

	got := search(t, c, url.Values{"path": {home}, "q": {"TAX"}})
	want := []string{"Documents/Tax Report 2025.pdf", "Documents/old/tax notes.txt", "tax-folder"}
	if len(got.names) != len(want) || got.total != 3 || got.truncated {
		t.Fatalf("got %+v, want %v (hidden and linked items skipped)", got, want)
	}
	for i := range want {
		if got.names[i] != want[i] {
			t.Fatalf("got %v, want %v", got.names, want)
		}
	}
	// Every word must match; hidden=1 includes dotfiles.
	if got := search(t, c, url.Values{"path": {home}, "q": {"tax report"}}); len(got.names) != 1 {
		t.Fatalf("two words: %v", got.names)
	}
	if got := search(t, c, url.Values{"path": {home}, "q": {"tax"}, "hidden": {"1"}}); len(got.names) != 4 {
		t.Fatalf("with hidden: %v", got.names)
	}
	resp, _ := c.Get(api("/api/v1/fs/search", url.Values{"path": {home}, "q": {"  "}}))
	decode[apiError](t, resp, http.StatusBadRequest)
}
