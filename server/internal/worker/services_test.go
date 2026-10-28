package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// fakeSystemd stands in for one systemd manager: services pass through
// "activating" or "deactivating" for a couple of looks before settling.
type fakeSystemd struct {
	mu      sync.Mutex
	units   []unitStatus
	files   []unitFile
	calls   []string
	deny    bool
	pending map[string]int
	after   map[string][2]string
}

type fakeSystemdSource struct{ *fakeSystemd }

func (s fakeSystemdSource) units(context.Context) ([]unitStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fakeSystemd.units, nil
}

func (s fakeSystemdSource) unitFiles(context.Context) ([]unitFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.files, nil
}

func (s fakeSystemdSource) call(_ context.Context, method string, interactive bool, args ...any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if files, ok := args[0].([]string); ok {
		// EnableUnitFiles and DisableUnitFiles.
		s.calls = append(s.calls, fmt.Sprintf("%s %v interactive=%v", method, files, interactive))
		if s.deny {
			return dbus.Error{Name: "org.freedesktop.DBus.Error.AccessDenied"}
		}
		for i := range s.files {
			if filepath.Base(s.files[i].Path) == files[0] {
				s.files[i].State = map[string]string{"EnableUnitFilesWithFlags": "enabled", "DisableUnitFilesWithFlags": "disabled"}[method]
			}
		}
		return nil
	}
	name := args[0].(string)
	s.calls = append(s.calls, fmt.Sprintf("%s %s %s interactive=%v", method, name, args[1], interactive))
	if s.deny {
		return dbus.Error{Name: "org.freedesktop.DBus.Error.AccessDenied"}
	}
	final := map[string][2]string{"StartUnit": {"active", "running"}, "StopUnit": {"inactive", "dead"}, "RestartUnit": {"active", "running"}}[method]
	if f, ok := s.after[name]; ok {
		final = f
	}
	s.pending[name] = 2
	for i := range s.fakeSystemd.units {
		if s.fakeSystemd.units[i].Name == name {
			s.fakeSystemd.units[i].Active, s.fakeSystemd.units[i].Sub = final[0], final[1]
			return nil
		}
	}
	// Starting a unit file systemd had not loaded loads it.
	s.fakeSystemd.units = append(s.fakeSystemd.units, unitStatus{Name: name, Load: "loaded", Active: final[0], Sub: final[1]})
	return nil
}

func (s fakeSystemdSource) state(_ context.Context, name string) (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending[name] > 0 {
		s.pending[name]--
		return "activating", "start", nil
	}
	for _, u := range s.fakeSystemd.units {
		if u.Name == name {
			return u.Active, u.Sub, nil
		}
	}
	return "inactive", "dead", nil
}

func testSystemd() *fakeSystemd {
	return &fakeSystemd{
		units: []unitStatus{
			{Name: "ssh.service", Description: "OpenBSD Secure Shell server", Load: "loaded", Active: "active", Sub: "running"},
			{Name: "broken.service", Description: "Breaks", Load: "loaded", Active: "failed", Sub: "failed"},
			{Name: "getty@tty1.service", Description: "Getty on tty1", Load: "loaded", Active: "active", Sub: "running"},
			{Name: "ghost.service", Load: "not-found", Active: "inactive", Sub: "dead"},
			{Name: "dev-sda.device", Load: "loaded", Active: "active", Sub: "plugged"},
		},
		files: []unitFile{
			{Path: "/usr/lib/systemd/system/ssh.service", State: "enabled"},
			{Path: "/usr/lib/systemd/system/getty@.service", State: "enabled"},
			{Path: "/usr/lib/systemd/system/cups.service", State: "disabled"},
			{Path: "/etc/systemd/system/broken.service", State: "static"},
		},
		pending: map[string]int{},
		after:   map[string][2]string{"broken.service": {"failed", "failed"}},
	}
}

func servicesWorker(t *testing.T, system, user *fakeSystemd) *Worker {
	t.Helper()
	w := driveWorker(t, nil)
	w.services.src = map[string]systemdSource{}
	w.services.failedAt = map[string]time.Time{"user": time.Now(), "system": time.Now()}
	if system != nil {
		w.services.src["system"] = fakeSystemdSource{system}
	}
	if user != nil {
		w.services.src["user"] = fakeSystemdSource{user}
	}
	return w
}

func TestServicesList(t *testing.T) {
	w := servicesWorker(t, testSystemd(), nil)
	rec := serve(t, w, "GET", "/api/v1/services?scope=system", "")
	var res struct {
		Available bool
		Services  []Service
	}
	json.Unmarshal(rec.Body.Bytes(), &res)
	want := []Service{
		{Name: "broken.service", Description: "Breaks", Load: "loaded", Active: "failed", Sub: "failed", Boot: "static"},
		{Name: "cups.service", Active: "inactive", Sub: "dead", Boot: "disabled"},
		{Name: "getty@tty1.service", Description: "Getty on tty1", Load: "loaded", Active: "active", Sub: "running"},
		{Name: "ssh.service", Description: "OpenBSD Secure Shell server", Load: "loaded", Active: "active", Sub: "running", Boot: "enabled"},
	}
	if rec.Code != http.StatusOK || !res.Available || !reflect.DeepEqual(res.Services, want) {
		t.Fatalf("%d %+v\nwant %+v", rec.Code, res, want)
	}

	// No user manager (no session bus): an empty, unavailable list.
	rec = serve(t, w, "GET", "/api/v1/services?scope=user", "")
	json.Unmarshal(rec.Body.Bytes(), &res)
	if rec.Code != http.StatusOK || res.Available || len(res.Services) != 0 {
		t.Fatalf("user: %d %s", rec.Code, rec.Body)
	}
	if rec := serve(t, w, "GET", "/api/v1/services?scope=other", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad scope: %d", rec.Code)
	}
}

func TestServiceActions(t *testing.T) {
	system, user := testSystemd(), testSystemd()
	w := servicesWorker(t, system, user)
	act := func(scope, name, action string) (int, map[string]string) {
		rec := serve(t, w, "POST", "/api/v1/services/action", fmt.Sprintf(`{"scope":%q,"name":%q,"action":%q}`, scope, name, action))
		var res map[string]string
		json.Unmarshal(rec.Body.Bytes(), &res)
		return rec.Code, res
	}

	// Waits through "activating", and says how it ended.
	if code, res := act("system", "cups.service", "start"); code != http.StatusOK || res["active"] != "active" || res["sub"] != "running" {
		t.Fatalf("start: %d %v", code, res)
	}
	if code, res := act("system", "broken.service", "restart"); code != http.StatusOK || res["active"] != "failed" {
		t.Fatalf("restart broken: %d %v", code, res)
	}
	if code, res := act("user", "ssh.service", "stop"); code != http.StatusOK || res["active"] != "inactive" {
		t.Fatalf("user stop: %d %v", code, res)
	}
	// polkit may only ask with Tilde's agent running (not in tests); the
	// user's own manager never asks.
	if want := []string{"StartUnit cups.service replace interactive=false", "RestartUnit broken.service replace interactive=false"}; !reflect.DeepEqual(system.calls, want) {
		t.Fatalf("system calls %v", system.calls)
	}
	if want := []string{"StopUnit ssh.service replace interactive=false"}; !reflect.DeepEqual(user.calls, want) {
		t.Fatalf("user calls %v", user.calls)
	}

	for _, bad := range [][3]string{
		{"system", "nope.service", "start"},
		{"system", "getty@.service", "start"},
		{"system", "ssh.service", "enable"},
		{"nowhere", "ssh.service", "start"},
	} {
		if code, _ := act(bad[0], bad[1], bad[2]); code != http.StatusBadRequest {
			t.Errorf("%v: %d", bad, code)
		}
	}

	system.deny = true
	if code, res := act("system", "ssh.service", "stop"); code != http.StatusForbidden || res["code"] != "not_authorized" {
		t.Fatalf("denied: %d %v", code, res)
	}
}

func TestParseJournal(t *testing.T) {
	out := []byte(`{"__REALTIME_TIMESTAMP":"1793208129098098","PRIORITY":"3","SYSLOG_IDENTIFIER":"cupsd","_PID":"812","MESSAGE":"failed to bind"}
{"__REALTIME_TIMESTAMP":"1793208130000000","MESSAGE":[104,105,255]}

not json
`)
	got := parseJournal(out)
	want := []JournalEntry{
		{Time: 1793208129098, Priority: 3, Ident: "cupsd", PID: 812, Message: "failed to bind"},
		{Time: 1793208130000, Priority: 6, Message: "hi\uFFFD"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

// fakeJournalctl stands in for journalctl: it records its arguments and
// prints one entry, and the hint for users who may read only their own logs.
func fakeJournalctl(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	script := filepath.Join(dir, "journalctl")
	os.WriteFile(script, []byte(`#!/bin/sh
echo "$@" > `+args+`
echo '{"__REALTIME_TIMESTAMP":"1000000","PRIORITY":"6","MESSAGE":"Started."}'
echo 'Hint: You are currently not seeing messages from other users and the system.' >&2
`), 0o755)
	old := journalCommand
	journalCommand = script
	t.Cleanup(func() { journalCommand = old })
	return args
}

func TestServiceJournal(t *testing.T) {
	argsFile := fakeJournalctl(t)
	w := servicesWorker(t, testSystemd(), testSystemd())
	rec := serve(t, w, "GET", "/api/v1/services/journal?scope=system&name=ssh.service", "")
	var res struct {
		Entries []JournalEntry
		Limited bool
	}
	json.Unmarshal(rec.Body.Bytes(), &res)
	if rec.Code != http.StatusOK || len(res.Entries) != 1 || res.Entries[0].Message != "Started." || !res.Limited {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	args, _ := os.ReadFile(argsFile)
	if got := strings.TrimSpace(string(args)); got != "--no-pager -o json -n 200 -u ssh.service" {
		t.Fatalf("args %q", got)
	}
	serve(t, w, "GET", "/api/v1/services/journal?scope=user&name=ssh.service", "")
	args, _ = os.ReadFile(argsFile)
	if !strings.HasPrefix(string(args), "--user ") {
		t.Fatalf("user args %q", args)
	}
	// Only services the manager lists: no options smuggled in as a name.
	for _, name := range []string{"--help", "nope.service", ""} {
		if rec := serve(t, w, "GET", "/api/v1/services/journal?scope=system&name="+name, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("%q: %d", name, rec.Code)
		}
	}
}

func TestServiceBoot(t *testing.T) {
	system := testSystemd()
	w := servicesWorker(t, system, nil)
	boot := func(name string, on bool) (int, map[string]string) {
		rec := serve(t, w, "POST", "/api/v1/services/boot", fmt.Sprintf(`{"scope":"system","name":%q,"enabled":%v}`, name, on))
		var res map[string]string
		json.Unmarshal(rec.Body.Bytes(), &res)
		return rec.Code, res
	}
	if code, res := boot("cups.service", true); code != http.StatusOK || res["boot"] != "enabled" {
		t.Fatalf("enable: %d %v", code, res)
	}
	if code, res := boot("cups.service", false); code != http.StatusOK || res["boot"] != "disabled" {
		t.Fatalf("disable: %d %v", code, res)
	}
	want := []string{"EnableUnitFilesWithFlags [cups.service] interactive=false", "DisableUnitFilesWithFlags [cups.service] interactive=false"}
	if !reflect.DeepEqual(system.calls, want) {
		t.Fatalf("calls %v", system.calls)
	}
	if code, _ := boot("nope.service", true); code != http.StatusBadRequest {
		t.Fatalf("unknown: %d", code)
	}
	system.deny = true
	if code, res := boot("ssh.service", false); code != http.StatusForbidden || res["code"] != "not_authorized" {
		t.Fatalf("denied: %d %v", code, res)
	}
}
