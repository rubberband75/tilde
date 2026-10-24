package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"tilde/internal/protocol"
)

// The server's own conversation with a session's worker, on routes under
// /internal/ that the proxy never forwards: the user's idle lock delay
// (D40), and the logind session's lock (D41).

const (
	// sessionRetry is how long to wait before asking the worker again
	// after a request for the server itself failed.
	sessionRetry = 30 * time.Second
	// maxSessionLine bounds a line of the worker's session stream. The
	// worker runs as the user, who could make it send anything: this
	// root process must never buffer much of it.
	maxSessionLine = 4096
	// maxLockAfter clamps the idle delay, in seconds: past a day, a lock
	// could only come after the token's 12 hours anyway.
	maxLockAfter = 24 * 60 * 60
)

// followSession reads the worker's session stream until the session ends:
// the user's lock settings (the worker reads them; the server never opens
// user files), on connect and whenever they change, and logind's requests
// to lock the session.
func (us *UserSession) followSession() {
	for {
		err := us.readSession()
		select {
		case <-us.done:
			return
		default:
		}
		slog.Warn("session stream from the worker", "user", us.User, "err", err)
		select {
		case <-us.done:
			return
		case <-time.After(sessionRetry):
		}
	}
}

func (us *UserSession) readSession() error {
	req, err := http.NewRequest(http.MethodGet, "http://worker/internal/session", nil)
	if err != nil {
		return err
	}
	req.Header.Set(protocol.InternalHeader, "1")
	resp, err := us.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	lines := bufio.NewScanner(resp.Body)
	lines.Buffer(make([]byte, 0, 1024), maxSessionLine)
	var lastLock time.Time
	var budget auditBudget
	for lines.Scan() {
		var ev struct {
			Type      string               `json:"type"`
			Source    string               `json:"source"`
			LockAfter int64                `json:"lockAfter"`
			Audit     *protocol.AuditEvent `json:"audit"`
		}
		if err := json.Unmarshal(lines.Bytes(), &ev); err != nil {
			return err
		}
		switch ev.Type {
		case "settings":
			d := time.Duration(min(max(ev.LockAfter, 0), maxLockAfter)) * time.Second
			if old := us.lockAfter.Swap(&d); old == nil || *old != d {
				slog.Info("idle lock", "user", us.User, "after", d, "source", ev.Source)
			}
		case "lock":
			// One a second is plenty; a flood from the user's own worker
			// must not keep every user's tokens waiting on their lock.
			if now := time.Now(); now.Sub(lastLock) >= time.Second && us.lockRequested != nil {
				lastLock = now
				us.lockRequested()
			}
		case "audit":
			if ev.Audit != nil {
				us.recordAudit(*ev.Audit, &budget)
			}
		}
	}
	if err := lines.Err(); err != nil {
		return err
	}
	return io.EOF
}

// recordAudit writes an event the worker reported (D46) under the
// session's user, within the user's budget; what was dropped, here or in
// the worker, is counted on the next record that gets through.
func (us *UserSession) recordAudit(ev protocol.AuditEvent, budget *auditBudget) {
	// Unknown events count too, or they could flood the server's log.
	if !budget.allow(time.Now()) {
		budget.dropped += ev.Dropped
		return
	}
	r, ok := workerRecord(us.User, ev)
	if !ok {
		slog.Warn("audit: unknown event from the worker", "user", us.User, "event", printable(ev.Event))
		return
	}
	if dropped := budget.dropped + ev.Dropped; dropped > 0 {
		r.Fields = append(r.Fields, [2]string{"DROPPED", strconv.Itoa(dropped)})
		budget.dropped = 0
	}
	us.audit.record(r)
}

// notifyLocked wakes followLockedHint: the lock state of the user's
// browsers may have changed.
func (us *UserSession) notifyLocked() {
	select {
	case us.hint <- struct{}{}:
	default: // a wake-up is pending already
	}
}

// followLockedHint keeps the logind session's LockedHint equal to whether
// every browser of the user is locked, until the session ends. A failed
// update is tried again, with one timer backing off up to sessionRetry.
func (us *UserSession) followLockedHint() {
	sent := false // logind's default
	var retry *time.Timer
	var retryC <-chan time.Time
	backoff := time.Second
	for {
		select {
		case <-us.done:
			if retry != nil {
				retry.Stop()
			}
			return
		case <-us.hint:
		case <-retryC:
			retry, retryC = nil, nil
		}
		locked := us.allLocked()
		if locked == sent {
			continue
		}
		if err := us.putLocked(locked); err != nil {
			if us.conn != nil && us.conn.IsClosed() {
				return // logging out: the session is going anyway
			}
			slog.Warn("set logind's LockedHint", "user", us.User, "err", err)
			if retry == nil {
				retry = time.NewTimer(backoff)
				retryC = retry.C
				backoff = min(2*backoff, sessionRetry)
			}
			continue
		}
		sent, backoff = locked, time.Second
	}
}

func (us *UserSession) putLocked(locked bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	body := strings.NewReader(fmt.Sprintf(`{"locked":%t}`, locked))
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, "http://worker/internal/session/locked", body)
	if err != nil {
		return err
	}
	req.Header.Set(protocol.InternalHeader, "1")
	req.Header.Set("Content-Type", "application/json")
	resp, err := us.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}
