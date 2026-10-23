package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"log/slog"
	"sync"
	"time"
)

// TokenStore keeps browser session tokens in memory, server-side, so they
// can be revoked (logout) and locked (D40), and are never readable by page
// script (they travel in an HttpOnly cookie). Tokens are stored by SHA-256
// hash, so the map holds nothing directly usable.
//
// A token is one client: a browser, whose tabs share the cookie. A locked
// token reaches nothing but /api/auth/*, and locking it ends the worker
// requests already made with it, WebSockets included, through its context.
// Clients lock on their own after the user's idle delay without input, and
// all of a user's clients lock together from the Lock Screen button. Each
// unlocks with the password, which replaces its token.
//
// A server restart forgets every token. That matches D17: in v1 a
// restart also ends every worker, so the browser is sent back to login.
type TokenStore struct {
	mu     sync.Mutex
	m      map[string]*tokenInfo
	maxAge time.Duration
	now    func() time.Time
	// lockAfter returns how long a user's clients may go without input
	// before they lock; 0 never locks. Nil never locks either.
	lockAfter func(user string) time.Duration
}

type tokenInfo struct {
	user string
	// created is when the password was last typed (login or unlock); the
	// absolute lifetime counts from there.
	created   time.Time
	lastSeen  time.Time
	lastInput time.Time
	locked    bool
	// unlockTries counts password checks for this token (maxUnlockTries).
	unlockTries int
	ctx         context.Context
	cancel      context.CancelFunc
}

// Token is what a valid cookie stands for.
type Token struct {
	User   string
	Locked bool
	// Context ends when the token is locked, revoked or expires; requests
	// to the worker made with the token end with it.
	Context context.Context
}

// NewTokenStore returns a store whose tokens expire maxAge after creation
// (the absolute timeout, D16).
func NewTokenStore(maxAge time.Duration) *TokenStore {
	return &TokenStore{m: map[string]*tokenInfo{}, maxAge: maxAge, now: time.Now}
}

func hashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// heartbeat is how often a browser reports input while there is some, so
// the server's last input time is at most this late. Idle locking allows
// for twice that.
func heartbeat(lockAfter time.Duration) time.Duration {
	if lockAfter <= 0 {
		return 15 * time.Second
	}
	return min(15*time.Second, max(time.Second, lockAfter/10))
}

// New creates a token for user.
func (s *TokenStore) New(user string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.newLocked(user)
}

func (s *TokenStore) newLocked(user string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := base64.RawURLEncoding.EncodeToString(b)
	now := s.now()
	ctx, cancel := context.WithCancel(context.Background())
	s.m[hashToken(tok)] = &tokenInfo{user: user, created: now, lastSeen: now, lastInput: now, ctx: ctx, cancel: cancel}
	return tok, nil
}

// get returns the live token for tok, dropping it if it has expired and
// locking it if its client has been idle too long. s.mu must be held.
func (s *TokenStore) get(tok string, now time.Time) (*tokenInfo, string, bool) {
	if tok == "" {
		return nil, "", false
	}
	h := hashToken(tok)
	ti, ok := s.m[h]
	if !ok {
		return nil, "", false
	}
	if now.Sub(ti.created) > s.maxAge {
		ti.cancel()
		delete(s.m, h)
		return nil, "", false
	}
	if !ti.locked && s.idle(ti, now) {
		s.lock(ti, "idle")
	}
	return ti, h, true
}

// idle reports whether ti's client has gone without input for longer than
// its user's delay, allowing for a heartbeat still on its way.
func (s *TokenStore) idle(ti *tokenInfo, now time.Time) bool {
	if s.lockAfter == nil {
		return false
	}
	d := s.lockAfter(ti.user)
	return d > 0 && now.Sub(ti.lastInput) > d+2*heartbeat(d)
}

func (s *TokenStore) lock(ti *tokenInfo, reason string) {
	ti.locked = true
	ti.cancel()
	slog.Info("lock", "user", ti.user, "reason", reason)
}

func (ti *tokenInfo) view() Token {
	return Token{User: ti.user, Locked: ti.locked, Context: ti.ctx}
}

// Lookup returns what a valid token stands for and records that it was
// used. Reading or streaming is not input: it does not keep a client from
// locking.
func (s *TokenStore) Lookup(tok string) (Token, bool) {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	ti, _, ok := s.get(tok, now)
	if !ok {
		return Token{}, false
	}
	ti.lastSeen = now
	return ti.view(), true
}

// Touch records keyboard or pointer input from the token's client. Input
// reported after the client has locked does not unlock it.
func (s *TokenStore) Touch(tok string) (Token, bool) {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	ti, _, ok := s.get(tok, now)
	if !ok {
		return Token{}, false
	}
	ti.lastSeen = now
	if !ti.locked {
		ti.lastInput = now
	}
	return ti.view(), true
}

// LockUser locks every client of user (the Lock Screen button).
func (s *TokenStore) LockUser(user, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ti := range s.m {
		if ti.user == user && !ti.locked {
			s.lock(ti, reason)
		}
	}
}

// maxUnlockTries is how many passwords a locked browser may try before it
// is signed out and must log in again.
const maxUnlockTries = 5

// BeginUnlock counts a password check for tok before it runs. Past
// maxUnlockTries it revokes the token instead and returns false.
func (s *TokenStore) BeginUnlock(tok string) bool {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	ti, h, ok := s.get(tok, now)
	if !ok {
		return false
	}
	ti.unlockTries++
	if ti.unlockTries > maxUnlockTries {
		ti.cancel()
		delete(s.m, h)
		return false
	}
	return true
}

// Rotate replaces tok with a new, unlocked token for the same user, after
// the password was checked. The new token's lifetime starts now. It fails
// if tok has gone in the meantime.
func (s *TokenStore) Rotate(tok string) (string, bool) {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	ti, h, ok := s.get(tok, now)
	if !ok {
		return "", false
	}
	ti.cancel()
	delete(s.m, h)
	fresh, err := s.newLocked(ti.user)
	if err != nil {
		return "", false
	}
	return fresh, true
}

// Revoke deletes one token.
func (s *TokenStore) Revoke(tok string) {
	h := hashToken(tok)
	s.mu.Lock()
	if ti, ok := s.m[h]; ok {
		ti.cancel()
		delete(s.m, h)
	}
	s.mu.Unlock()
}

// RevokeUser deletes every token for user.
func (s *TokenStore) RevokeUser(user string) {
	s.mu.Lock()
	for h, ti := range s.m {
		if ti.user == user {
			ti.cancel()
			delete(s.m, h)
		}
	}
	s.mu.Unlock()
}

// Count returns the number of live tokens for user (the tray's "clients
// connected" indicator can use this later).
func (s *TokenStore) Count(user string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, ti := range s.m {
		if ti.user == user {
			n++
		}
	}
	return n
}

// Sweep removes expired tokens and locks idle ones, so a client that has
// stopped making requests (a tab left open on an unattended machine) loses
// its open WebSockets on time.
func (s *TokenStore) Sweep() {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for h, ti := range s.m {
		if now.Sub(ti.created) > s.maxAge {
			ti.cancel()
			delete(s.m, h)
		} else if !ti.locked && s.idle(ti, now) {
			s.lock(ti, "idle")
		}
	}
}
