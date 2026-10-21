package worker

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestMimeSniff(t *testing.T) {
	dir := t.TempDir()
	elf, err := os.ReadFile("/bin/true")
	if err != nil {
		t.Skip("no /bin/true")
	}
	files := map[string]struct {
		content string
		mode    os.FileMode
		want    string
	}{
		"program":    {string(elf), 0o755, "application/x-executable"},
		"backup":     {"#!/usr/bin/env python3\nprint(1)\n", 0o755, "text/x-python"},
		"deploy":     {"#!/bin/bash\necho\n", 0o755, "application/x-shellscript"},
		"README":     {"Plain words here.\n", 0o644, "text/plain"},
		"empty":      {"", 0o644, "application/x-zerosize"},
		"picture":    {"\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR", 0o644, "image/png"},
		"blob":       {"\x00\x01\x02\x03\xff\xfe", 0o644, "application/octet-stream"},
		"notes.txt":  {"\x00\x01binary but named .txt", 0o644, "text/plain"},
		"run-me.zzz": {"\x00\x01\x02", 0o755, "application/x-executable"},
	}
	for name, f := range files {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(f.content), f.mode)
		os.Chmod(p, f.mode)
		lst, _ := os.Lstat(p)
		if got := entryFor(p, lst).MIME; got != f.want {
			t.Errorf("%s: %q, want %q", name, got, f.want)
		}
	}

	// A listing stops sniffing after its budget.
	budget := 1
	readme, _ := os.Lstat(filepath.Join(dir, "README"))
	entryWithBudget(filepath.Join(dir, "README"), readme, &budget)
	if got := entryWithBudget(filepath.Join(dir, "README"), readme, &budget).MIME; got != "application/octet-stream" || budget != 0 {
		t.Errorf("after the budget: %q (budget %d)", got, budget)
	}
}

func TestStreamList(t *testing.T) {
	c, home := startWorker(t)
	dir := filepath.Join(home, "many")
	os.Mkdir(dir, 0o755)
	for i := 0; i < 2500; i++ {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%04d.txt", i)), nil, 0o644)
	}
	resp, err := c.Get(api("/api/v1/fs/list", url.Values{"path": {dir}, "stream": {"1"}}))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/x-ndjson" {
		t.Fatalf("status %d, type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	var lines []map[string]json.RawMessage
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(nil, 4<<20)
	for sc.Scan() {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, m)
	}
	if len(lines) != 5 || string(lines[0]["path"]) != fmt.Sprintf("%q", dir) || string(lines[4]["total"]) != "2500" {
		t.Fatalf("got %d lines: first %v, last %v", len(lines), lines[0], lines[len(lines)-1])
	}
	seen := 0
	for _, l := range lines[1:4] {
		var es []Entry
		json.Unmarshal(l["entries"], &es)
		seen += len(es)
	}
	if seen != 2500 {
		t.Fatalf("%d entries streamed", seen)
	}

	// Errors before streaming are ordinary JSON errors.
	resp, _ = c.Get(api("/api/v1/fs/list", url.Values{"path": {filepath.Join(home, "nope")}, "stream": {"1"}}))
	decode[apiError](t, resp, http.StatusNotFound)
}
