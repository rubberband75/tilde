package server

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"tilde/internal/protocol"
)

// The audit log (D46): who logged in from where, and what they did through
// Tilde. Root writes every record to the system journal, tagged
// tilde-audit, with the facility authpriv, so rsyslog also files it in
// /var/log/auth.log next to sshd's:
//
//	journalctl -t tilde-audit
//	journalctl -t tilde-audit TILDE_USER=alice TILDE_EVENT=delete
//
// The server records what it sees itself (logins, locks, sessions); the
// worker reports what the user does (terminals, deletions, mounts, polkit)
// on its session stream, and the server writes those under the session's
// user. Without a journal (containers, development), records go to the
// server's log.

// journalSocket is journald's native protocol socket; tests replace it.
var journalSocket = "/run/systemd/journal/socket"

// auditRecord is one entry of the audit log.
type auditRecord struct {
	Event string
	User  string
	Rhost string
	// Text says what happened, after the user's name: "logged in".
	Text string
	// Failed marks a refusal or failure (journal priority warning).
	Failed bool
	// Fields are extra TILDE_* fields; a name may repeat (TILDE_PATH).
	Fields [][2]string
}

// message is the record as one line of text, as syslog files show it.
// Paths and user names come from users: control characters are escaped,
// so nobody can forge a line in /var/log/auth.log.
func (r auditRecord) message() string {
	user := r.User
	if user == "" {
		user = "(unknown)"
	}
	m := user + " " + r.Text
	if r.Rhost != "" {
		m += " from " + r.Rhost
	}
	return printable(m)
}

func printable(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return r == utf8.RuneError || unicode.IsControl(r) }) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case unicode.IsControl(r):
			q := strconv.QuoteRune(r)
			b.WriteString(q[1 : len(q)-1])
		default:
			b.WriteString(s[i : i+n])
		}
		i += n
	}
	return b.String()
}

// auditLog writes records to the journal.
type auditLog struct {
	mu   sync.Mutex
	conn net.Conn
	// sink receives every record instead of the journal, in tests.
	sink func(auditRecord)
}

func (a *auditLog) record(r auditRecord) {
	if a == nil {
		return
	}
	// A failed login's user name is whatever was typed.
	if len(r.User) > 64 {
		r.User = strings.ToValidUTF8(r.User[:64], "") + "…"
	}
	if a.sink != nil {
		a.sink(r)
		return
	}
	priority := "5" // notice
	if r.Failed {
		priority = "4" // warning
	}
	fields := [][2]string{
		{"MESSAGE", r.message()},
		{"PRIORITY", priority},
		{"SYSLOG_IDENTIFIER", "tilde-audit"},
		{"SYSLOG_FACILITY", "10"}, // authpriv
		{"TILDE_EVENT", r.Event},
		{"TILDE_USER", r.User},
	}
	if r.Rhost != "" {
		fields = append(fields, [2]string{"TILDE_RHOST", r.Rhost})
	}
	for _, f := range r.Fields {
		fields = append(fields, [2]string{"TILDE_" + f[0], f[1]})
	}
	if err := a.send(journalEntry(fields)); err != nil {
		attrs := []any{"event", r.Event, "user", r.User, "rhost", r.Rhost}
		for _, f := range r.Fields {
			attrs = append(attrs, strings.ToLower(f[0]), f[1])
		}
		slog.Info("audit: "+r.message(), attrs...)
	}
}

func (a *auditLog) send(entry []byte) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for try := 0; try < 2; try++ {
		if a.conn == nil {
			c, err := net.Dial("unixgram", journalSocket)
			if err != nil {
				return err
			}
			a.conn = c
		}
		a.conn.SetWriteDeadline(time.Now().Add(time.Second))
		if _, err := a.conn.Write(entry); err == nil {
			return nil
		}
		// journald restarted: dial again, once.
		a.conn.Close()
		a.conn = nil
	}
	return fmt.Errorf("journal unreachable")
}

// journalEntry encodes fields in journald's native protocol: KEY=value
// lines, or for values with a line break, the key, a line break, the
// value's length as 64-bit little endian, the value and a line break.
func journalEntry(fields [][2]string) []byte {
	var b bytes.Buffer
	for _, f := range fields {
		if !strings.Contains(f[1], "\n") {
			b.WriteString(f[0] + "=" + f[1] + "\n")
			continue
		}
		b.WriteString(f[0] + "\n")
		binary.Write(&b, binary.LittleEndian, uint64(len(f[1])))
		b.WriteString(f[1] + "\n")
	}
	return b.Bytes()
}

// workerRecord turns an event from user's worker into a record, or false
// for an event the server does not know.
func workerRecord(user string, ev protocol.AuditEvent) (auditRecord, bool) {
	r := auditRecord{Event: ev.Event, User: user, Rhost: ev.Rhost}
	items := func(verb string) string {
		n := max(ev.Count, len(ev.Paths))
		text := fmt.Sprintf("%s %d item", verb, n)
		if n != 1 {
			text += "s"
		}
		if len(ev.Paths) > 0 {
			quoted := make([]string, len(ev.Paths))
			for i, p := range ev.Paths {
				quoted[i] = strconv.Quote(p)
			}
			text += ": " + strings.Join(quoted, ", ")
			if n > len(ev.Paths) {
				text += ", …"
			}
		}
		r.Fields = append(r.Fields, [2]string{"COUNT", strconv.Itoa(n)})
		for _, p := range ev.Paths {
			r.Fields = append(r.Fields, [2]string{"PATH", p})
		}
		return text
	}
	volume := func(verb string) string {
		r.Fields = append(r.Fields, [2]string{"DEVICE", ev.Device}, [2]string{"VOLUME", ev.Name})
		return fmt.Sprintf("%s %q (%s)", verb, ev.Name, ev.Device)
	}
	switch ev.Event {
	case "terminal_open":
		r.Text = "opened terminal " + ev.Terminal
		r.Fields = append(r.Fields, [2]string{"TERMINAL", ev.Terminal})
	case "terminal_close":
		r.Text = "closed terminal " + ev.Terminal
		r.Fields = append(r.Fields, [2]string{"TERMINAL", ev.Terminal})
	case "trash":
		r.Text = items("moved to the trash")
	case "delete":
		r.Text = items("deleted permanently")
	case "empty_trash":
		r.Text = items("emptied the trash,")
	case "mount":
		r.Text = volume("mounted")
	case "unmount":
		r.Text = volume("unmounted")
	case "eject":
		r.Text = volume("ejected")
	case "polkit":
		r.Fields = append(r.Fields, [2]string{"ACTION", ev.Action}, [2]string{"IDENTITY", ev.Identity}, [2]string{"RESULT", ev.Result})
		switch ev.Result {
		case "granted":
			r.Text = fmt.Sprintf("authenticated as %s for %s", ev.Identity, ev.Action)
		case "failed":
			r.Text = fmt.Sprintf("failed to authenticate as %s for %s", ev.Identity, ev.Action)
			r.Failed = true
		default:
			return r, false
		}
	default:
		return r, false
	}
	return r, true
}

// auditBudget bounds the records one user's worker may have written: a
// burst, then a steady rate. The worker is the user's process, and must
// not be able to flood root's journal (journald would then drop records
// of other users too).
type auditBudget struct {
	tokens  float64
	last    time.Time
	dropped int
}

const (
	auditBurst = 200
	auditRate  = 20 // per second
)

// allow reports whether one more record may be written at now.
func (b *auditBudget) allow(now time.Time) bool {
	if b.last.IsZero() {
		b.tokens = auditBurst
	} else {
		b.tokens = min(auditBurst, b.tokens+now.Sub(b.last).Seconds()*auditRate)
	}
	b.last = now
	if b.tokens < 1 {
		b.dropped++
		return false
	}
	b.tokens--
	return true
}
