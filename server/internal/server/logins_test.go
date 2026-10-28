package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// fakeJournalctl prints records as journalctl -o json would, and keeps its
// arguments.
func fakeJournalctl(t *testing.T, lines string) string {
	t.Helper()
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	script := filepath.Join(dir, "journalctl")
	os.WriteFile(script, []byte("#!/bin/sh\necho \"$@\" > "+args+"\ncat <<'EOF'\n"+lines+"\nEOF\n"), 0o755)
	old := journalctlCommand
	journalctlCommand = script
	t.Cleanup(func() { journalctlCommand = old })
	return args
}

func TestLogins(t *testing.T) {
	argsFile := fakeJournalctl(t, `{"__REALTIME_TIMESTAMP":"1793220000000000","TILDE_EVENT":"login","TILDE_RHOST":"192.0.2.1","MESSAGE":"alice logged in from 192.0.2.1","PRIORITY":"5"}
{"__REALTIME_TIMESTAMP":"1793210000000000","TILDE_EVENT":"login_failed","TILDE_RHOST":"203.0.113.9","MESSAGE":"alice failed to log in from 203.0.113.9","PRIORITY":"4"}
not json`)
	lt := newLockTest(t, nil)
	tok := lt.login("alice")
	w := lt.do(http.MethodGet, "/api/auth/logins", tok, "")
	lt.expect(w, http.StatusOK, "")
	var res struct {
		Available bool
		Entries   []LoginRecord
	}
	json.Unmarshal(w.Body.Bytes(), &res)
	want := []LoginRecord{
		{Time: 1793220000000, Event: "login", Rhost: "192.0.2.1", Text: "logged in from 192.0.2.1"},
		{Time: 1793210000000, Event: "login_failed", Rhost: "203.0.113.9", Text: "failed to log in from 203.0.113.9", Failed: true},
	}
	if !res.Available || !reflect.DeepEqual(res.Entries, want) {
		t.Fatalf("got %+v", res)
	}
	args, _ := os.ReadFile(argsFile)
	for _, a := range []string{"-t tilde-audit", "_UID=" + strconv.Itoa(os.Geteuid()) + " TILDE_USER=alice", "TILDE_EVENT=login TILDE_EVENT=login_failed", "--reverse", "--output-fields="} {
		if !strings.Contains(string(args), a) {
			t.Errorf("args %q lack %q", args, a)
		}
	}

	// A locked browser cannot read it; nor can anyone without a session.
	lt.expect(lt.do(http.MethodPost, "/api/auth/lock", tok, ""), http.StatusNoContent, "")
	lt.expect(lt.do(http.MethodGet, "/api/auth/logins", tok, ""), http.StatusLocked, "locked")
	if w := lt.do(http.MethodGet, "/api/auth/logins", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no session: %d", w.Code)
	}
}

func TestLoginsWithoutJournal(t *testing.T) {
	old := journalctlCommand
	journalctlCommand = filepath.Join(t.TempDir(), "missing")
	t.Cleanup(func() { journalctlCommand = old })
	lt := newLockTest(t, nil)
	w := lt.do(http.MethodGet, "/api/auth/logins", lt.login("alice"), "")
	lt.expect(w, http.StatusOK, "")
	if !strings.Contains(w.Body.String(), `"available":false`) {
		t.Fatalf("%s", w.Body)
	}
}
