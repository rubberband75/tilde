package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"time"
)

// Recent logins (D72): the user's last logins and the attempts on their
// account, from the audit log (D46), for System Settings. The server reads
// it: root wrote it to the system journal, which the user may not read.
// It reads Tilde's own records, not the user's files.

// LoginRecord is one record, newest first in a list.
type LoginRecord struct {
	// Time in Unix milliseconds.
	Time  int64  `json:"time"`
	Event string `json:"event"` // login, login_failed, unlock_failed, rate_limited
	Rhost string `json:"rhost,omitempty"`
	// Text is the record's message, without the user's name in front.
	Text   string `json:"text"`
	Failed bool   `json:"failed"`
}

// journalctlCommand reads the journal; tests replace it.
var journalctlCommand = "journalctl"

// loginEvents are the records shown: logins, and attempts that failed.
var loginEvents = []string{"login", "login_failed", "unlock_failed", "rate_limited"}

const loginsLimit = 50

// loginsMaxOutput bounds what one read takes from journalctl: 50 records
// of four short fields fit many times over.
const loginsMaxOutput = 1 << 20

// loginsReaders bounds the journalctl processes running at once, as root,
// for everyone's System Settings together.
var loginsReaders = make(chan struct{}, 4)

// readLogins asks the journal for user's records. A failed login is
// recorded under the name that was typed, so the list shows attempts on
// this account and on no other.
func readLogins(ctx context.Context, user string) ([]LoginRecord, error) {
	select {
	case loginsReaders <- struct{}{}:
		defer func() { <-loginsReaders }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	// Anyone on the machine can send the journal an entry tagged
	// tilde-audit with any TILDE_ fields; _UID is journald's own, so this
	// takes only the records the server itself wrote.
	args := []string{"--no-pager", "--reverse", "-o", "json", "-n", strconv.Itoa(loginsLimit),
		"--output-fields=MESSAGE,PRIORITY,TILDE_EVENT,TILDE_RHOST",
		"-t", "tilde-audit", "_UID=" + strconv.Itoa(os.Geteuid()), "TILDE_USER=" + user}
	// Matches on one field are alternatives: any of these events.
	for _, ev := range loginEvents {
		args = append(args, "TILDE_EVENT="+ev)
	}
	cmd := exec.CommandContext(ctx, journalctlCommand, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	out := &io.LimitedReader{R: stdout, N: loginsMaxOutput}
	list := []LoginRecord{}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64*1024), loginsMaxOutput)
	for sc.Scan() {
		var e map[string]any
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		str := func(k string) string { s, _ := e[k].(string); return s }
		us, _ := strconv.ParseInt(str("__REALTIME_TIMESTAMP"), 10, 64)
		msg := str("MESSAGE")
		// "alice logged in from 192.0.2.1": the name is the reader's own.
		if len(msg) > len(user) && msg[:len(user)+1] == user+" " {
			msg = msg[len(user)+1:]
		}
		list = append(list, LoginRecord{
			Time: us / 1000, Event: str("TILDE_EVENT"), Rhost: str("TILDE_RHOST"),
			Text: msg, Failed: str("PRIORITY") == "4",
		})
	}
	if err := sc.Err(); err != nil {
		slog.Warn("reading recent logins", "user", user, "err", err)
	}
	if out.N <= 0 {
		cmd.Process.Kill() // past the bound: it would block writing the rest
	}
	// Nothing read: say why if journalctl failed (no journal, no access).
	if err := cmd.Wait(); err != nil && len(list) == 0 {
		return nil, err
	}
	return list, nil
}

// handleLogins lists the user's recent logins and failed attempts.
// `available` is false when the journal cannot be read (no systemd
// journal, or a development server running as the user).
func (s *Server) handleLogins(w http.ResponseWriter, r *http.Request) {
	_, t, ok := s.cookieToken(r)
	switch us := s.session(w, t, ok); {
	case us == nil:
	case t.Locked:
		writeLocked(w)
	default:
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		list, err := readLogins(ctx, t.User)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"available": false, "entries": []LoginRecord{}})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"available": true, "entries": list})
	}
}
