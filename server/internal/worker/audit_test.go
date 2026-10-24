package worker

import (
	"bufio"
	"encoding/json"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"tilde/internal/protocol"
)

// TestAuditEvents: what the user does through the worker reaches the
// server on the session stream, with the browser's address the server
// passed along (D46).
func TestAuditEvents(t *testing.T) {
	c, home := startWorker(t)
	req, _ := http.NewRequest(http.MethodGet, api("/internal/session", nil), nil)
	req.Header.Set(protocol.InternalHeader, "1")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	events := make(chan protocol.AuditEvent, 16)
	go func() {
		lines := bufio.NewScanner(resp.Body)
		for lines.Scan() {
			var ev sessionEvent
			if json.Unmarshal(lines.Bytes(), &ev) == nil && ev.Type == "audit" {
				events <- *ev.Audit
			}
		}
	}()
	next := func() protocol.AuditEvent {
		t.Helper()
		select {
		case ev := <-events:
			return ev
		case <-time.After(5 * time.Second):
			t.Fatal("no audit event")
			return protocol.AuditEvent{}
		}
	}
	send := func(method, path, body string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(method, api(path, nil), strings.NewReader(body))
		req.Header.Set(protocol.ClientHeader, "192.0.2.7")
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	a, b := filepath.Join(home, "a.txt"), filepath.Join(home, "b.txt")
	write(t, a, "a")
	write(t, b, "b")
	decode[any](t, send(http.MethodPost, "/api/v1/fs/trash", `{"paths":["`+a+`","`+home+`/missing"]}`), http.StatusOK)
	if ev := next(); !reflect.DeepEqual(ev, protocol.AuditEvent{Event: "trash", Rhost: "192.0.2.7", Paths: []string{a}, Count: 1}) {
		t.Fatalf("trash: %+v", ev)
	}
	decode[any](t, send(http.MethodPost, "/api/v1/trash/empty", ""), http.StatusNoContent)
	if ev := next(); !reflect.DeepEqual(ev, protocol.AuditEvent{Event: "empty_trash", Rhost: "192.0.2.7", Paths: []string{a}, Count: 1}) {
		t.Fatalf("empty trash: %+v", ev)
	}
	decode[any](t, send(http.MethodPost, "/api/v1/jobs", `{"op":"delete","sources":["`+b+`"]}`), http.StatusAccepted)
	if ev := next(); !reflect.DeepEqual(ev, protocol.AuditEvent{Event: "delete", Rhost: "192.0.2.7", Paths: []string{b}, Count: 1}) {
		t.Fatalf("delete: %+v", ev)
	}

	term := decode[TerminalInfo](t, send(http.MethodPost, "/api/v1/terminals", ""), http.StatusCreated)
	if ev := next(); ev.Event != "terminal_open" || ev.Terminal != term.ID || ev.Rhost != "192.0.2.7" {
		t.Fatalf("terminal open: %+v", ev)
	}
	decode[any](t, send(http.MethodDelete, "/api/v1/terminals/"+term.ID, ""), http.StatusNoContent)
	if ev := next(); ev.Event != "terminal_close" || ev.Terminal != term.ID {
		t.Fatalf("terminal close: %+v", ev)
	}
}

// TestAuditQueue: events wait for the stream; past the queue they are
// counted, and the count rides on the next event sent.
func TestAuditQueue(t *testing.T) {
	q := newAuditQueue()
	for range auditQueueSize + 5 {
		q.add(protocol.AuditEvent{Event: "trash"})
	}
	ev := q.next(<-q.ch)
	if ev.Dropped != 5 || q.next(<-q.ch).Dropped != 0 {
		t.Fatalf("dropped %d", ev.Dropped)
	}
}

func TestAuditPaths(t *testing.T) {
	many := make([]string, 50)
	for i := range many {
		many[i] = "/home/me/file"
	}
	if paths, n := auditPaths(many); len(paths) != maxAuditPaths || n != 50 {
		t.Fatalf("%d paths, count %d", len(paths), n)
	}
	// Control characters take six bytes each once encoded: still fits.
	long := []string{"/" + strings.Repeat("\x01", 3000), "/" + strings.Repeat("é", 2000), "/after"}
	paths, n := auditPaths(long)
	b, _ := json.Marshal(paths)
	if n != 3 || len(b) > maxAuditBytes+64 || !strings.HasSuffix(paths[0], "…") {
		t.Fatalf("%d bytes for %d paths of %d: %q", len(b), len(paths), n, paths)
	}
	for _, p := range paths {
		if !utf8.ValidString(p) {
			t.Fatalf("cut inside a character: %q", p)
		}
	}
}
