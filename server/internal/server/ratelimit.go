package server

import (
	"sync"
	"time"
)

// loginLimiter slows down password guessing per client address. Per-account
// lockout is PAM's job (pam_faillock); this covers spraying many accounts
// from one address.
type loginLimiter struct {
	mu       sync.Mutex
	failures map[string][]time.Time
	max      int
	window   time.Duration
	now      func() time.Time
}

func newLoginLimiter(max int, window time.Duration) *loginLimiter {
	return &loginLimiter{failures: map[string][]time.Time{}, max: max, window: window, now: time.Now}
}

// Allowed reports whether addr may try again, and if not, how long to wait.
func (l *loginLimiter) Allowed(addr string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	recent := l.prune(addr)
	if len(recent) < l.max {
		return true, 0
	}
	return false, l.window - l.now().Sub(recent[0])
}

// Fail records a failed attempt.
func (l *loginLimiter) Fail(addr string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failures[addr] = append(l.prune(addr), l.now())
}

// Succeed clears the record for addr.
func (l *loginLimiter) Succeed(addr string) {
	l.mu.Lock()
	delete(l.failures, addr)
	l.mu.Unlock()
}

func (l *loginLimiter) prune(addr string) []time.Time {
	cutoff := l.now().Add(-l.window)
	recent := l.failures[addr][:0]
	for _, t := range l.failures[addr] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) == 0 {
		delete(l.failures, addr)
		return nil
	}
	l.failures[addr] = recent
	return recent
}
