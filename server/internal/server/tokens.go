package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"sync"
	"time"
)

// TokenStore keeps browser session tokens in memory, server-side, so they
// can be revoked (logout, lock) and are never readable by page script (they
// travel in an HttpOnly cookie). Tokens are stored by SHA-256 hash, so the
// map holds nothing directly usable.
//
// A server restart forgets every token. That matches decision 4f: in v1 a
// restart also ends every worker, so the browser is sent back to login.
type TokenStore struct {
	mu     sync.Mutex
	m      map[string]*tokenInfo
	maxAge time.Duration
	now    func() time.Time
}

type tokenInfo struct {
	user     string
	created  time.Time
	lastSeen time.Time
}

// NewTokenStore returns a store whose tokens expire maxAge after creation
// (the absolute timeout, decision 4e).
func NewTokenStore(maxAge time.Duration) *TokenStore {
	return &TokenStore{m: map[string]*tokenInfo{}, maxAge: maxAge, now: time.Now}
}

func hashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// New creates a token for user.
func (s *TokenStore) New(user string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := base64.RawURLEncoding.EncodeToString(b)
	now := s.now()
	s.mu.Lock()
	s.m[hashToken(tok)] = &tokenInfo{user: user, created: now, lastSeen: now}
	s.mu.Unlock()
	return tok, nil
}

// Lookup returns the user for a valid token and records activity.
func (s *TokenStore) Lookup(tok string) (string, bool) {
	if tok == "" {
		return "", false
	}
	h := hashToken(tok)
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	ti, ok := s.m[h]
	if !ok {
		return "", false
	}
	if now.Sub(ti.created) > s.maxAge {
		delete(s.m, h)
		return "", false
	}
	ti.lastSeen = now
	return ti.user, true
}

// Revoke deletes one token.
func (s *TokenStore) Revoke(tok string) {
	s.mu.Lock()
	delete(s.m, hashToken(tok))
	s.mu.Unlock()
}

// RevokeUser deletes every token for user.
func (s *TokenStore) RevokeUser(user string) {
	s.mu.Lock()
	for h, ti := range s.m {
		if ti.user == user {
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

// Sweep removes expired tokens.
func (s *TokenStore) Sweep() {
	now := s.now()
	s.mu.Lock()
	for h, ti := range s.m {
		if now.Sub(ti.created) > s.maxAge {
			delete(s.m, h)
		}
	}
	s.mu.Unlock()
}
