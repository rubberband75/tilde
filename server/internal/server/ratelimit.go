package server

import (
	"sync"
	"time"
)

// loginLimiter slows down password guessing per client address; logins and
// unlocks share it. Per-account lockout is PAM's job (pam_faillock); this
// covers spraying many accounts from one address.
//
// An attempt counts from the moment it starts, before PAM answers, so
// parallel requests cannot all get in before the first failure is
// recorded. A right password takes back that user's attempts from the
// address, and only that user's: logging in to some other account from the
// same address does not reset the count.
type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string][]attempt
	max      int
	window   time.Duration
	now      func() time.Time
}

type attempt struct {
	at   time.Time
	user string
}

func newLoginLimiter(max int, window time.Duration) *loginLimiter {
	return &loginLimiter{attempts: map[string][]attempt{}, max: max, window: window, now: time.Now}
}

// Begin counts an attempt from addr for user, or refuses it while addr has
// max attempts in the window, saying how long to wait. The attempt stays
// counted as a failure unless Succeed or Cancel takes it back.
func (l *loginLimiter) Begin(addr, user string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	recent := l.prune(addr)
	if len(recent) >= l.max {
		return false, l.window - l.now().Sub(recent[0].at)
	}
	l.attempts[addr] = append(recent, attempt{at: l.now(), user: user})
	return true, 0
}

// Succeed takes back user's attempts from addr: the password was right.
func (l *loginLimiter) Succeed(addr, user string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.attempts[addr][:0]
	for _, a := range l.attempts[addr] {
		if a.user != user {
			kept = append(kept, a)
		}
	}
	l.set(addr, kept)
}

// Cancel takes back user's latest attempt from addr, when it ended without
// a verdict on the password (an expired account, an internal error).
func (l *loginLimiter) Cancel(addr, user string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	list := l.attempts[addr]
	for i := len(list) - 1; i >= 0; i-- {
		if list[i].user == user {
			l.set(addr, append(list[:i], list[i+1:]...))
			return
		}
	}
}

func (l *loginLimiter) prune(addr string) []attempt {
	cutoff := l.now().Add(-l.window)
	recent := l.attempts[addr][:0]
	for _, a := range l.attempts[addr] {
		if a.at.After(cutoff) {
			recent = append(recent, a)
		}
	}
	l.set(addr, recent)
	return recent
}

func (l *loginLimiter) set(addr string, list []attempt) {
	if len(list) == 0 {
		delete(l.attempts, addr)
		return
	}
	l.attempts[addr] = list
}
