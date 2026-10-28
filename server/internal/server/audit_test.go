package server

import (
	"bytes"
	"encoding/binary"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"tilde/internal/protocol"
)

// recorder captures audit records.
type recorder struct{ records []auditRecord }

func (rec *recorder) events() []string {
	var out []string
	for _, r := range rec.records {
		out = append(out, r.Event)
	}
	return out
}

func (rec *recorder) last() auditRecord { return rec.records[len(rec.records)-1] }

func auditTest(t *testing.T, worker http.Handler) (*lockTest, *recorder) {
	lt := newLockTest(t, worker)
	rec := &recorder{}
	lt.s.audit.sink = func(r auditRecord) { rec.records = append(rec.records, r) }
	return lt, rec
}

// TestAuditLogins: the server records logins, failures and the rate
// limit, locks and unlocks, and signing out, with the browser's address
// (D46).
func TestAuditLogins(t *testing.T) {
	lt, rec := auditTest(t, noContent)
	lt.expect(lt.send(http.MethodPost, "/api/auth/login", `{"username":"alice","password":"wrong"}`), http.StatusUnauthorized, "")
	lt.expect(lt.send(http.MethodPost, "/api/auth/login", `{"username":"no\nbody","password":"x"}`), http.StatusUnauthorized, "")
	w := lt.send(http.MethodPost, "/api/auth/login", `{"username":"alice","password":"secret"}`)
	lt.expect(w, http.StatusOK, "")
	tok := cookie(w, lt.s.cookie).Value
	lt.expect(lt.do(http.MethodPost, "/api/auth/lock", tok, ""), http.StatusNoContent, "")
	lt.expect(lt.do(http.MethodPost, "/api/auth/unlock", tok, `{"password":"wrong"}`), http.StatusUnauthorized, "")
	w = lt.do(http.MethodPost, "/api/auth/unlock", tok, `{"password":"secret"}`)
	lt.expect(w, http.StatusOK, "")
	lt.expect(lt.do(http.MethodPost, "/api/auth/leave", cookie(w, lt.s.cookie).Value, ""), http.StatusNoContent, "")

	want := []string{"login_failed", "login_failed", "login", "lock", "unlock_failed", "unlock", "leave"}
	if got := rec.events(); !slices.Equal(got, want) {
		t.Fatalf("events %v, want %v", got, want)
	}
	for _, r := range rec.records {
		if r.User == "" || r.Rhost != "192.0.2.1" {
			t.Errorf("%s: user %q, rhost %q", r.Event, r.User, r.Rhost)
		}
	}
	if r := rec.records[1]; r.message() != `no\nbody failed to log in from 192.0.2.1` || !r.Failed {
		t.Errorf("a typed user name with a line break: %q", r.message())
	}
	if r := rec.records[3]; !slices.Contains(r.Fields, [2]string{"REASON", "manual"}) {
		t.Errorf("lock fields %v", r.Fields)
	}

	// Locked out: recorded once per request, within the budget for what
	// anyone can cause.
	for range 6 {
		lt.send(http.MethodPost, "/api/auth/login", `{"username":"alice","password":"wrong"}`)
	}
	if rec.last().Event != "rate_limited" {
		t.Fatalf("last %+v", rec.last())
	}
}

// TestAuditIdleAndLogind: locks nobody asked for in a request are
// recorded with their reason.
func TestAuditIdleAndLogind(t *testing.T) {
	lt, rec := auditTest(t, noContent)
	lt.login("alice")
	lt.now = lt.now.Add(time.Hour)
	lt.s.tokens.Sweep()
	lt.s.sessions.lockRequested("alice")
	if got := rec.events(); !slices.Equal(got, []string{"lock", "lock"}) {
		t.Fatalf("events %v", got)
	}
	if !slices.Contains(rec.records[0].Fields, [2]string{"REASON", "idle"}) || !slices.Contains(rec.records[1].Fields, [2]string{"REASON", "logind"}) {
		t.Fatalf("records %+v", rec.records)
	}
}

// TestAuditFromWorker: the worker's events are written under the
// session's user, only the known ones, within the user's budget.
func TestAuditFromWorker(t *testing.T) {
	lt, rec := auditTest(t, noContent)
	lt.us.audit = lt.s.audit
	var budget auditBudget
	lt.us.recordAudit(protocol.AuditEvent{Event: "delete", Rhost: "192.0.2.1", Paths: []string{"/home/alice/a\nb", "/home/alice/c"}, Count: 30}, &budget)
	r := rec.last()
	if r.User != "alice" || r.Event != "delete" ||
		r.message() != `alice deleted permanently 30 items: "/home/alice/a\nb", "/home/alice/c", … from 192.0.2.1` {
		t.Fatalf("record %+v: %s", r, r.message())
	}
	if !slices.Contains(r.Fields, [2]string{"PATH", "/home/alice/a\nb"}) || !slices.Contains(r.Fields, [2]string{"COUNT", "30"}) {
		t.Fatalf("fields %v", r.Fields)
	}

	n := len(rec.records)
	lt.us.recordAudit(protocol.AuditEvent{Event: "login", Rhost: "192.0.2.1"}, &budget)
	lt.us.recordAudit(protocol.AuditEvent{Event: "polkit", Result: "granted?"}, &budget)
	if len(rec.records) != n {
		t.Fatal("the worker made the server write an event it does not own")
	}

	for range auditBurst + 10 {
		lt.us.recordAudit(protocol.AuditEvent{Event: "terminal_open", Terminal: "t1"}, &budget)
	}
	if got := len(rec.records) - n; got > auditBurst {
		t.Fatalf("%d records from one burst", got)
	}
	budget.last = budget.last.Add(-time.Second)
	lt.us.recordAudit(protocol.AuditEvent{Event: "terminal_close", Terminal: "t1", Dropped: 3}, &budget)
	// 200 tokens: the delete, the two refused events, 197 terminals; 13
	// terminals dropped here, 3 in the worker.
	if r := rec.last(); r.Event != "terminal_close" || !slices.Contains(r.Fields, [2]string{"DROPPED", "16"}) {
		t.Fatalf("after the burst: %+v", r)
	}
}

func TestAuditNetworkFromWorker(t *testing.T) {
	for _, tc := range []struct {
		ev     protocol.AuditEvent
		want   string
		failed bool
	}{
		{protocol.AuditEvent{Event: "network_connect", Name: "HomeNet", Device: "wlp2s0", Result: "connected"},
			`alice connected wlp2s0 to "HomeNet" from 192.0.2.1`, false},
		{protocol.AuditEvent{Event: "network_connect", Name: "Cafe", Device: "wlp2s0", Result: "wrong_password"},
			`alice failed to connect wlp2s0 to "Cafe" (wrong_password) from 192.0.2.1`, true},
		{protocol.AuditEvent{Event: "network_disconnect", Name: "Wired connection 1", Device: "enp3s0"},
			`alice disconnected enp3s0 from "Wired connection 1" from 192.0.2.1`, false},
		{protocol.AuditEvent{Event: "wifi_off"}, `alice turned Wi-Fi off from 192.0.2.1`, false},
		{protocol.AuditEvent{Event: "wifi_on"}, `alice turned Wi-Fi on from 192.0.2.1`, false},
		{protocol.AuditEvent{Event: "service_restart", Name: "cups.service", Result: "active"},
			`alice restarted system service cups.service (now active) from 192.0.2.1`, false},
		{protocol.AuditEvent{Event: "service_enable", Name: "cups.service", Result: "enabled"},
			`alice enabled system service cups.service at boot (now enabled) from 192.0.2.1`, false},
		{protocol.AuditEvent{Event: "service_start", Name: "broken.service", Result: "failed"},
			`alice started system service broken.service (now failed) from 192.0.2.1`, true},
	} {
		tc.ev.Rhost = "192.0.2.1"
		r, ok := workerRecord("alice", tc.ev)
		if !ok || r.message() != tc.want || r.Failed != tc.failed {
			t.Errorf("%s: %v %q failed=%v, want %q failed=%v", tc.ev.Event, ok, r.message(), r.Failed, tc.want, tc.failed)
		}
	}
}

// TestJournal: records reach journald's socket in its native protocol,
// values with line breaks in the binary form.
func TestJournal(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "journal")
	ln, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: sock, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	old := journalSocket
	journalSocket = sock
	t.Cleanup(func() { journalSocket = old })

	a := &auditLog{}
	a.record(auditRecord{Event: "delete", User: "alice", Rhost: "192.0.2.1", Text: `deleted permanently 1 item: "/tmp/a\nb"`,
		Fields: [][2]string{{"PATH", "/tmp/a\nb"}}})
	buf := make([]byte, 4096)
	ln.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := ln.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	got := buf[:n]
	for _, line := range []string{
		"MESSAGE=alice deleted permanently 1 item: \"/tmp/a\\nb\" from 192.0.2.1\n",
		"SYSLOG_IDENTIFIER=tilde-audit\n", "SYSLOG_FACILITY=10\n", "PRIORITY=5\n",
		"TILDE_EVENT=delete\n", "TILDE_USER=alice\n", "TILDE_RHOST=192.0.2.1\n",
	} {
		if !bytes.Contains(got, []byte(line)) {
			t.Errorf("missing %q in %q", line, got)
		}
	}
	var size [8]byte
	binary.LittleEndian.PutUint64(size[:], uint64(len("/tmp/a\nb")))
	if !bytes.Contains(got, append(append([]byte("TILDE_PATH\n"), size[:]...), "/tmp/a\nb\n"...)) {
		t.Errorf("path not in binary form: %q", got)
	}
}

// TestClientHeader: the worker learns the browser's address from the
// server, never from the browser.
func TestClientHeader(t *testing.T) {
	var seen string
	worker := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get(protocol.ClientHeader)
		w.WriteHeader(http.StatusNoContent)
	})
	lt := newLockTest(t, newWorkerProxy(handlerTransport{worker}))
	req := httptest.NewRequest(http.MethodGet, "http://desk.lan/api/v1/hello", nil)
	req.RemoteAddr = lt.addr
	req.AddCookie(&http.Cookie{Name: lt.s.cookie, Value: lt.login("alice")})
	req.Header.Set(protocol.ClientHeader, "203.0.113.66")
	w := httptest.NewRecorder()
	lt.h.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent || seen != "192.0.2.1" {
		t.Fatalf("%d, worker saw %q", w.Code, seen)
	}
}

func TestPrintable(t *testing.T) {
	for in, want := range map[string]string{
		"plain café":     "plain café",
		"a\nb\tc":        `a\nb\tc`,
		"bad\xffbyte":    `bad\xffbyte`,
		"esc\x1b[31mred": `esc\x1b[31mred`,
		"c1\u0085next":   `c1\u0085next`,
	} {
		if got := printable(in); got != want {
			t.Errorf("printable(%q) = %q, want %q", in, got, want)
		}
	}
}
