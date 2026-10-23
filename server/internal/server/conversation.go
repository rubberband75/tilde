package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"tilde/internal/protocol"
)

// Logins and unlocks can take more than the password: PAM may ask for a
// verification code (D43). The check then waits in the session helper,
// and the browser answers with POST /api/auth/answer, holding the check by
// a short-lived HttpOnly cookie of its own.

// check is a password check in progress: the Manager's *Auth, or a fake in
// tests.
type check interface {
	Prompt() *protocol.Prompt
	Session() *UserSession
	Answer(ctx context.Context, answer string) error
	Cancel()
}

// pendingTimeout is how long a check waits for its answer; tests shorten it.
var pendingTimeout = 2 * time.Minute

// Bounds on the checks waiting at once, each a root helper: in all, per
// user and per address, so one account cannot keep everyone else's
// two-factor logins out.
const (
	maxPending        = 64
	maxPendingPerUser = 2
	maxPendingPerAddr = 4
)

// pending is a login or unlock waiting for an answer.
type pending struct {
	mu    sync.Mutex // one answer at a time
	check check
	// unlock: an unlock of user's locked browser; else a login.
	unlock bool
	user   string
	addr   string
	timer  *time.Timer
	// answered counts answers sent to PAM; the first rides on the
	// attempt the login or unlock counted, later ones count on their own.
	answered int
}

// checkContext is the context a check runs under: never the request's.
// A browser that disconnects mid-check must not kill the helper, which
// would leave no verdict to count (D43).
func checkContext(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(r.Context()), stepTimeout+30*time.Second)
}

type pendingStore struct {
	mu sync.Mutex
	m  map[string]*pending // by hash of the cookie value
}

// add holds p for its browser and returns the cookie value, or "" when too
// many checks are waiting. The check is cancelled if no answer comes.
func (ps *pendingStore) add(p *pending) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	tok := base64.RawURLEncoding.EncodeToString(b)
	h := hashToken(tok)
	ps.mu.Lock()
	defer ps.mu.Unlock()
	users, addrs := 0, 0
	for _, q := range ps.m {
		if q.user == p.user {
			users++
		}
		if q.addr == p.addr {
			addrs++
		}
	}
	if len(ps.m) >= maxPending || users >= maxPendingPerUser || addrs >= maxPendingPerAddr {
		return ""
	}
	ps.m[h] = p
	p.timer = time.AfterFunc(pendingTimeout, func() {
		if ps.take(tok) == p {
			p.mu.Lock()
			p.check.Cancel()
			p.mu.Unlock()
		}
	})
	return tok
}

// get returns the check held by tok, or nil.
func (ps *pendingStore) get(tok string) *pending {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.m[hashToken(tok)]
}

// take removes and returns the check held by tok, or nil.
func (ps *pendingStore) take(tok string) *pending {
	h := hashToken(tok)
	ps.mu.Lock()
	defer ps.mu.Unlock()
	p := ps.m[h]
	delete(ps.m, h)
	return p
}

func (s *Server) authCookie(value string, maxAge int) *http.Cookie {
	c := s.sessionCookie(value, maxAge)
	c.Name = s.cookie + "-auth"
	return c
}

// stop ends a check that is no longer wanted. Its attempt is taken back
// only if no answer reached PAM: the password was right, nothing was
// guessed. p.mu must be held, and p taken from the store by the caller.
func (s *Server) stop(p *pending) {
	p.timer.Stop()
	p.check.Cancel()
	if p.answered == 0 {
		s.limiter.Cancel(p.addr, p.user)
	}
}

// dropPending stops the check this browser was waiting on, if any, before
// it starts a new login or unlock.
func (s *Server) dropPending(r *http.Request) {
	c, err := r.Cookie(s.authCookie("", 0).Name)
	if err != nil {
		return
	}
	if p := s.pending.get(c.Value); p != nil && p.mu.TryLock() {
		if s.pending.take(c.Value) == p {
			s.stop(p)
		}
		p.mu.Unlock()
	}
}

// carryOn answers a check that passed its first step: it either waits for
// the answer to PAM's question, or is done.
func (s *Server) carryOn(w http.ResponseWriter, r *http.Request, p *pending) {
	prompt := p.check.Prompt()
	if prompt == nil {
		s.complete(w, r, p)
		return
	}
	tok := s.pending.add(p)
	if tok == "" {
		p.check.Cancel()
		writeJSON(w, http.StatusServiceUnavailable, apiError{Error: "Too many logins in progress. Try again in a minute.", Code: "busy"})
		return
	}
	http.SetCookie(w, s.authCookie(tok, int(pendingTimeout.Seconds())))
	writeJSON(w, http.StatusOK, map[string]*protocol.Prompt{"prompt": prompt})
}

// handleAnswer passes the browser's answer to the question its check is
// waiting on, or cancels the check.
func (s *Server) handleAnswer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Answer string `json:"answer"`
		Cancel bool   `json:"cancel"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	if err := jsonDecode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid request", Code: "bad_request"})
		return
	}
	c, err := r.Cookie(s.authCookie("", 0).Name)
	var p *pending
	if err == nil {
		p = s.pending.get(c.Value)
	}
	if p == nil {
		writeJSON(w, http.StatusUnauthorized, apiError{Error: "That took too long. Start again.", Code: "auth_expired"})
		return
	}
	if !p.mu.TryLock() {
		writeJSON(w, http.StatusConflict, apiError{Error: "still checking the last answer", Code: "busy"})
		return
	}
	defer p.mu.Unlock()
	if s.pending.get(c.Value) != p {
		// Finished by another request while this one waited.
		writeJSON(w, http.StatusUnauthorized, apiError{Error: "That took too long. Start again.", Code: "auth_expired"})
		return
	}
	if req.Cancel {
		if s.pending.take(c.Value) == p {
			s.stop(p)
		}
		http.SetCookie(w, s.authCookie("", -1))
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// A module that asks again after a wrong answer gets no free guesses:
	// answers after the first count as attempts of their own.
	if p.answered > 0 {
		if !s.beginAttempt(w, p.addr, p.user) {
			return
		}
		if tok, err := r.Cookie(s.cookie); p.unlock && (err != nil || !s.tokens.BeginUnlock(tok.Value)) {
			s.pending.take(c.Value)
			p.timer.Stop()
			p.check.Cancel()
			s.clearCookie(w)
			http.SetCookie(w, s.authCookie("", -1))
			writeJSON(w, http.StatusUnauthorized, apiError{Error: "Too many wrong passwords. Log in again.", Code: "too_many_attempts"})
			return
		}
	}
	p.answered++

	ctx, cancel := checkContext(r)
	defer cancel()
	err = p.check.Answer(ctx, req.Answer)
	req.Answer = ""
	if err == nil && p.check.Prompt() != nil {
		writeJSON(w, http.StatusOK, map[string]*protocol.Prompt{"prompt": p.check.Prompt()})
		return
	}
	s.pending.take(c.Value)
	p.timer.Stop()
	http.SetCookie(w, s.authCookie("", -1))
	if err != nil {
		s.checkFailed(w, p, err, "Incorrect password or code.")
		return
	}
	s.complete(w, r, p)
}

// checkFailed answers a failed check. Only a definite verdict other than a
// wrong password or code (an expired account) takes the attempt back: once
// the secret reached PAM, nothing else may, or an attacker could make a
// check fail on purpose to guess for free.
func (s *Server) checkFailed(w http.ResponseWriter, p *pending, err error, wrong string) {
	what := "login"
	if p.unlock {
		what = "unlock"
	}
	var ae *AuthError
	if errors.As(err, &ae) {
		status := http.StatusForbidden
		msg := ae.Message
		if ae.Code == protocol.CodeAuthFailed {
			status = http.StatusUnauthorized
			msg = wrong
		} else {
			s.limiter.Cancel(p.addr, p.user)
		}
		slog.Warn(what+" failed", "user", p.user, "rhost", p.addr, "code", ae.Code)
		writeJSON(w, status, apiError{Error: msg, Code: ae.Code})
		return
	}
	slog.Error(what+": could not check the password", "user", p.user, "err", err)
	writeJSON(w, http.StatusInternalServerError, apiError{Error: "Could not log you in. See the server log.", Code: protocol.CodeInternal})
}

// complete ends a check that passed: a login gets a token for the user's
// session, an unlock replaces the browser's locked token.
func (s *Server) complete(w http.ResponseWriter, r *http.Request, p *pending) {
	s.limiter.Succeed(p.addr, p.user)
	if p.unlock {
		s.completeUnlock(w, r, p)
		return
	}
	us := p.check.Session()
	tok, err := s.tokens.New(us.User)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "could not create a session token", Code: protocol.CodeInternal})
		return
	}
	// The new cookie replaces this browser's old one; drop its token too.
	if old, err := r.Cookie(s.cookie); err == nil {
		s.tokens.Revoke(old.Value)
	}
	http.SetCookie(w, s.sessionCookie(tok, int(s.cfg.SessionMaxAge.Seconds())))
	slog.Info("login", "user", us.User, "rhost", p.addr)
	writeJSON(w, http.StatusOK, s.info(us, false))
}

func (s *Server) completeUnlock(w http.ResponseWriter, r *http.Request, p *pending) {
	tok, t, ok := s.cookieToken(r)
	us := s.session(w, t, ok)
	if us == nil {
		return
	}
	if t.User != p.user {
		// The browser logged in as someone else meanwhile.
		writeJSON(w, http.StatusUnauthorized, apiError{Error: "not logged in", Code: "not_logged_in"})
		return
	}
	fresh, ok := s.tokens.Rotate(tok)
	if !ok {
		// Not clearing the cookie: another tab of this browser may have
		// unlocked at the same moment and set a fresh one.
		writeJSON(w, http.StatusUnauthorized, apiError{Error: "not logged in", Code: "not_logged_in"})
		return
	}
	http.SetCookie(w, s.sessionCookie(fresh, int(s.cfg.SessionMaxAge.Seconds())))
	slog.Info("unlock", "user", t.User, "rhost", p.addr)
	writeJSON(w, http.StatusOK, s.info(us, false))
}
