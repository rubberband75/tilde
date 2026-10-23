package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tilde/internal/protocol"
)

// cookie returns the cookie name set by w, or nil.
func cookie(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func promptOf(t *testing.T, w *httptest.ResponseRecorder) *protocol.Prompt {
	t.Helper()
	var body struct {
		Prompt *protocol.Prompt `json:"prompt"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Prompt == nil {
		t.Fatalf("no prompt: %d %s", w.Code, w.Body)
	}
	return body.Prompt
}

// TestLoginWithCode: when PAM asks for a verification code after the
// password, the browser gets the question and answers it, holding the
// check by its own cookie; only then is it logged in (D43).
func TestLoginWithCode(t *testing.T) {
	lt := newLockTest(t, noContent)
	lt.code = "123456"
	authName := lt.s.cookie + "-auth"
	login := `{"username":"alice","password":"secret"}`

	w := lt.send(http.MethodPost, "/api/auth/login", login)
	if p := promptOf(t, w); p.Text != "Verification code:" {
		t.Fatalf("prompt %+v", p)
	}
	auth := cookie(w, authName)
	if auth == nil || !auth.HttpOnly || auth.SameSite != http.SameSiteStrictMode || cookie(w, lt.s.cookie) != nil {
		t.Fatalf("cookies %v", w.Result().Cookies())
	}

	// A wrong code ends the check: start again.
	w = lt.send(http.MethodPost, "/api/auth/answer", `{"answer":"000000"}`, auth)
	lt.expect(w, http.StatusUnauthorized, protocol.CodeAuthFailed)
	if c := cookie(w, authName); c == nil || c.MaxAge >= 0 {
		t.Fatal("the check's cookie was not cleared")
	}
	lt.expect(lt.send(http.MethodPost, "/api/auth/answer", `{"answer":"123456"}`, auth), http.StatusUnauthorized, "auth_expired")

	w = lt.send(http.MethodPost, "/api/auth/login", login)
	promptOf(t, w)
	w = lt.send(http.MethodPost, "/api/auth/answer", `{"answer":"123456"}`, cookie(w, authName))
	lt.expect(w, http.StatusOK, "")
	session := cookie(w, lt.s.cookie)
	if session == nil {
		t.Fatal("no session cookie after the code")
	}
	lt.expect(lt.do(http.MethodGet, "/api/v1/hello", session.Value, ""), http.StatusNoContent, "")
}

// TestUnlockWithCode: unlocking asks for the code too (D43).
func TestUnlockWithCode(t *testing.T) {
	lt := newLockTest(t, noContent)
	lt.code = "654321"
	a := lt.login("alice")
	lt.expect(lt.do(http.MethodPost, "/api/auth/lock", a, ""), http.StatusNoContent, "")

	w := lt.do(http.MethodPost, "/api/auth/unlock", a, `{"password":"secret"}`)
	promptOf(t, w)
	lt.expect(lt.do(http.MethodGet, "/api/v1/hello", a, ""), http.StatusLocked, "locked")
	w = lt.send(http.MethodPost, "/api/auth/answer", `{"answer":"654321"}`,
		cookie(w, lt.s.cookie+"-auth"), &http.Cookie{Name: lt.s.cookie, Value: a})
	lt.expect(w, http.StatusOK, "")
	fresh := cookie(w, lt.s.cookie)
	if fresh == nil || fresh.Value == a {
		t.Fatal("unlocking did not replace the cookie")
	}
	lt.expect(lt.do(http.MethodGet, "/api/v1/hello", fresh.Value, ""), http.StatusNoContent, "")
	lt.expect(lt.do(http.MethodGet, "/api/v1/hello", a, ""), http.StatusUnauthorized, "not_logged_in")
}

// TestCancelAndTimeout: a check waiting for an answer can be cancelled,
// and is cancelled when no answer comes.
func TestCancelAndTimeout(t *testing.T) {
	lt := newLockTest(t, noContent)
	lt.code = "111111"
	var checks []*fakeCheck
	login := lt.s.login
	lt.s.login = func(ctx context.Context, user, password, addr string) (check, error) {
		c, err := login(ctx, user, password, addr)
		if c != nil {
			checks = append(checks, c.(*fakeCheck))
		}
		return c, err
	}
	authName := lt.s.cookie + "-auth"
	body := `{"username":"alice","password":"secret"}`

	lt.expect(lt.send(http.MethodPost, "/api/auth/answer", `{"answer":"111111"}`), http.StatusUnauthorized, "auth_expired")

	w := lt.send(http.MethodPost, "/api/auth/login", body)
	auth := cookie(w, authName)
	lt.expect(lt.send(http.MethodPost, "/api/auth/answer", `{"cancel":true}`, auth), http.StatusNoContent, "")
	if !checks[0].cancelled {
		t.Fatal("cancel did not stop the check")
	}
	lt.expect(lt.send(http.MethodPost, "/api/auth/answer", `{"answer":"111111"}`, auth), http.StatusUnauthorized, "auth_expired")

	old := pendingTimeout
	pendingTimeout = 50 * time.Millisecond
	t.Cleanup(func() { pendingTimeout = old })
	w = lt.send(http.MethodPost, "/api/auth/login", body)
	auth = cookie(w, authName)
	deadline := time.Now().Add(5 * time.Second)
	for lt.s.pending.get(auth.Value) != nil {
		if time.Now().After(deadline) {
			t.Fatal("the check did not time out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	lt.expect(lt.send(http.MethodPost, "/api/auth/answer", `{"answer":"111111"}`, auth), http.StatusUnauthorized, "auth_expired")
}

// TestDisconnectStillCounts: a wrong guess counts even if the browser
// hangs up before PAM answers; otherwise hanging up early on every slow
// (wrong) answer would make guessing free.
func TestDisconnectStillCounts(t *testing.T) {
	lt := newLockTest(t, noContent)
	lt.s.login = func(ctx context.Context, _, _, _ string) (check, error) {
		time.Sleep(20 * time.Millisecond) // PAM's delay after a failure
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &AuthError{Code: protocol.CodeAuthFailed, Message: "authentication failed"}
	}
	for range 5 {
		ctx, cancel := context.WithCancel(context.Background())
		req := httptest.NewRequest(http.MethodPost, "http://desk.lan/api/auth/login", strings.NewReader(`{"username":"alice","password":"guess"}`))
		req.Header.Set("Origin", "http://desk.lan")
		req.RemoteAddr = lt.addr
		req = req.WithContext(ctx)
		go func() { time.Sleep(5 * time.Millisecond); cancel() }()
		lt.h.ServeHTTP(httptest.NewRecorder(), req)
	}
	lt.expect(lt.send(http.MethodPost, "/api/auth/login", `{"username":"alice","password":"secret"}`), http.StatusTooManyRequests, "rate_limited")
}

// TestRepeatedAnswersCount: a module that asks again after a wrong answer
// gets no free guesses, and cancelling after an answer gives nothing back.
func TestRepeatedAnswersCount(t *testing.T) {
	lt := newLockTest(t, noContent)
	lt.s.login = func(context.Context, string, string, string) (check, error) {
		return &fakeCheck{prompts: []string{"Passcode:"}, answers: []string{"424242"}, again: true, us: lt.us}, nil
	}
	w := lt.send(http.MethodPost, "/api/auth/login", `{"username":"alice","password":"secret"}`)
	auth := cookie(w, lt.s.cookie+"-auth")
	// The login counted 1; the first answer rides on it, the next 4 count.
	for range 5 {
		promptOf(t, lt.send(http.MethodPost, "/api/auth/answer", `{"answer":"000000"}`, auth))
	}
	lt.expect(lt.send(http.MethodPost, "/api/auth/answer", `{"answer":"000000"}`, auth), http.StatusTooManyRequests, "rate_limited")
	lt.expect(lt.send(http.MethodPost, "/api/auth/answer", `{"cancel":true}`, auth), http.StatusNoContent, "")
	lt.expect(lt.send(http.MethodPost, "/api/auth/login", `{"username":"alice","password":"secret"}`), http.StatusTooManyRequests, "rate_limited")
}

// TestPendingLimits: one browser holds one check, and one user at most two,
// so nobody can fill the server with checks that wait for codes.
func TestPendingLimits(t *testing.T) {
	lt := newLockTest(t, noContent)
	lt.code = "123456"
	login := `{"username":"alice","password":"secret"}`
	authName := lt.s.cookie + "-auth"

	first := cookie(lt.send(http.MethodPost, "/api/auth/login", login), authName)
	// Starting again from the same browser replaces its check.
	again := cookie(lt.send(http.MethodPost, "/api/auth/login", login, first), authName)
	lt.expect(lt.send(http.MethodPost, "/api/auth/answer", `{"answer":"123456"}`, first), http.StatusUnauthorized, "auth_expired")

	lt.addr = "198.51.100.7:1234"
	promptOf(t, lt.send(http.MethodPost, "/api/auth/login", login))
	lt.addr = "198.51.100.8:1234"
	lt.expect(lt.send(http.MethodPost, "/api/auth/login", login), http.StatusServiceUnavailable, "busy")
	lt.expect(lt.send(http.MethodPost, "/api/auth/answer", `{"answer":"123456"}`, again), http.StatusOK, "")
}
