package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"log/slog"
	"slices"
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
	// changed is called, with mu held, when a user's tokens are added,
	// locked or removed (logind's LockedHint follows, D41).
	changed func(user string)
	// idleLocked is called, with mu held, when a client locks after its
	// user's idle delay (the audit log, D46).
	idleLocked func(user string)
}

type tokenInfo struct {
	user string
	// id names the browser in the list of the user's browsers, without
	// giving away the token; it, client and since survive unlocking.
	id     string
	client clientInfo
	// since is when this browser logged in.
	since time.Time
	// conns counts its open events sockets: a tab showing the desktop.
	conns int
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

// clientInfo is what the server knows of a browser: where it connects
// from and what it says it is.
type clientInfo struct {
	addr  string
	agent string
}

// New creates a token for user.
func (s *TokenStore) New(user string) (string, error) {
	return s.NewFor(user, clientInfo{})
}

// NewFor creates a token for user on a new browser.
func (s *TokenStore) NewFor(user string, c clientInfo) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := make([]byte, 8)
	if _, err := rand.Read(id); err != nil {
		return "", err
	}
	return s.newLocked(&tokenInfo{user: user, id: hex.EncodeToString(id), client: c, since: s.now()})
}

// newLocked stores a new token for the browser ti describes, filling in
// the rest. s.mu must be held.
func (s *TokenStore) newLocked(ti *tokenInfo) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := base64.RawURLEncoding.EncodeToString(b)
	now := s.now()
	ti.created, ti.lastSeen, ti.lastInput = now, now, now
	ti.ctx, ti.cancel = context.WithCancel(context.Background())
	s.m[hashToken(tok)] = ti
	s.notify(ti.user)
	return tok, nil
}

func (s *TokenStore) notify(user string) {
	if s.changed != nil {
		s.changed(user)
	}
}

// remove deletes the token stored under hash h. s.mu must be held.
func (s *TokenStore) remove(h string, ti *tokenInfo) {
	ti.cancel()
	delete(s.m, h)
	s.notify(ti.user)
}

// AllLocked reports whether no browser of user is unlocked: the logind
// session's LockedHint (D41). With no browsers at all, nobody can use the
// session without the password either.
func (s *TokenStore) AllLocked(user string) bool {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ti := range s.m {
		if ti.user == user && !ti.locked && now.Sub(ti.created) <= s.maxAge {
			return false
		}
	}
	return true
}

// get returns the live token for tok, dropping it if it has expired and
// locking it if its client has been idle too long. s.mu must be held.
func (s *TokenStore) get(tok string, now time.Time) (*tokenInfo, string, bool) {
	if tok == "" {
		return nil, "", false
	}
	return s.getHash(hashToken(tok), now)
}

// getHash is get by the token's hash. s.mu must be held.
func (s *TokenStore) getHash(h string, now time.Time) (*tokenInfo, string, bool) {
	ti, ok := s.m[h]
	if !ok {
		return nil, "", false
	}
	if now.Sub(ti.created) > s.maxAge {
		s.remove(h, ti)
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
	if reason == "idle" && s.idleLocked != nil {
		s.idleLocked(ti.user)
	}
	s.notify(ti.user)
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

// LookupHash is Lookup by the token's hash, for what holds a token's hash
// rather than the token (the app port's cookies, D76).
func (s *TokenStore) LookupHash(h string) (Token, bool) {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	ti, _, ok := s.getHash(h, now)
	if !ok {
		return Token{}, false
	}
	ti.lastSeen = now
	return ti.view(), true
}

// ValidHash reports whether a token with this hash is live, without
// counting as a use.
func (s *TokenStore) ValidHash(h string) bool {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _, ok := s.getHash(h, now)
	return ok
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
		s.remove(h, ti)
		return false
	}
	return true
}

// Rotate replaces tok with a new, unlocked token for the same user and
// browser, after the password was checked, from addr. The new token's
// lifetime starts now. It fails if tok has gone in the meantime.
func (s *TokenStore) Rotate(tok, addr string) (string, bool) {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	ti, h, ok := s.get(tok, now)
	if !ok {
		return "", false
	}
	s.remove(h, ti)
	c := ti.client
	if addr != "" {
		c.addr = addr
	}
	fresh, err := s.newLocked(&tokenInfo{user: ti.user, id: ti.id, client: c, since: ti.since})
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
		s.remove(h, ti)
	}
	s.mu.Unlock()
}

// RevokeUser deletes every token for user.
func (s *TokenStore) RevokeUser(user string) {
	s.mu.Lock()
	for h, ti := range s.m {
		if ti.user == user {
			s.remove(h, ti)
		}
	}
	s.mu.Unlock()
}

// Client is one of a user's browsers, as the panel's list shows it.
type Client struct {
	ID      string    `json:"id"`
	Address string    `json:"address"`
	Agent   string    `json:"agent"`
	Since   time.Time `json:"since"`
	// LastSeen is the browser's last request; LastInput its last
	// keyboard or pointer input.
	LastSeen  time.Time `json:"lastSeen"`
	LastInput time.Time `json:"lastInput"`
	Locked    bool      `json:"locked"`
	// Connected: a tab shows the desktop now (its events socket is open).
	Connected bool `json:"connected"`
	// Current is the browser asking.
	Current bool `json:"current"`
}

// Clients lists user's browsers that are signed in: the one tok belongs
// to first, then the most recently seen.
func (s *TokenStore) Clients(user, tok string) []Client {
	now := s.now()
	mine := hashToken(tok)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Client{}
	for h, ti := range s.m {
		if ti.user != user || now.Sub(ti.created) > s.maxAge {
			continue
		}
		out = append(out, Client{
			ID: ti.id, Address: ti.client.addr, Agent: ti.client.agent, Since: ti.since,
			LastSeen: ti.lastSeen, LastInput: ti.lastInput,
			Locked: ti.locked || s.idle(ti, now), Connected: ti.conns > 0, Current: h == mine,
		})
	}
	slices.SortFunc(out, func(a, b Client) int {
		if a.Current != b.Current {
			if a.Current {
				return -1
			}
			return 1
		}
		return b.LastSeen.Compare(a.LastSeen)
	})
	return out
}

// RevokeID signs out user's browser id, and returns where it was.
func (s *TokenStore) RevokeID(user, id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for h, ti := range s.m {
		if ti.user == user && ti.id == id && id != "" {
			s.remove(h, ti)
			return ti.client.addr, true
		}
	}
	return "", false
}

// Connect counts an open events socket of tok's browser until release is
// called: the panel shows which browsers have a tab open.
func (s *TokenStore) Connect(tok string) (release func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ti, ok := s.m[hashToken(tok)]
	if !ok {
		return func() {}
	}
	ti.conns++
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		ti.conns--
	}
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
			s.remove(h, ti)
		} else if !ti.locked && s.idle(ti, now) {
			s.lock(ti, "idle")
		}
	}
}
