package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// clientReq sends a same-origin request from a browser at addr that says
// it is agent, with tok as its cookie.
func (lt *lockTest) clientReq(method, path, tok, addr, agent, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://desk.lan"+path, strings.NewReader(body))
	req.Header.Set("Origin", "http://desk.lan")
	req.Header.Set("User-Agent", agent)
	req.RemoteAddr = addr + ":5000"
	if tok != "" {
		req.AddCookie(&http.Cookie{Name: lt.s.cookie, Value: tok})
	}
	w := httptest.NewRecorder()
	lt.h.ServeHTTP(w, req)
	return w
}

func (lt *lockTest) clients(tok string) []Client {
	lt.t.Helper()
	w := lt.do(http.MethodGet, "/api/auth/clients", tok, "")
	lt.expect(w, http.StatusOK, "")
	var res struct{ Clients []Client }
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		lt.t.Fatal(err)
	}
	return res.Clients
}

// TestClients: each browser the user signed in is listed with where it
// connects from and what it says it is, the asking one first; the list
// says which have a tab open, and keeps a browser's entry across an
// unlock (D47).
func TestClients(t *testing.T) {
	opened := make(chan struct{})
	closeEvents := make(chan struct{})
	worker := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/events" {
			opened <- struct{}{}
			<-closeEvents
		}
		w.WriteHeader(http.StatusNoContent)
	})
	lt, rec := auditTest(t, worker)
	login := func(addr, agent string) string {
		w := lt.clientReq(http.MethodPost, "/api/auth/login", "", addr, agent, `{"username":"alice","password":"secret"}`)
		lt.expect(w, http.StatusOK, "")
		return cookie(w, lt.s.cookie).Value
	}
	home := login("192.0.2.10", "Firefox at home")
	work := login("198.51.100.20", "Chrome at work")

	list := lt.clients(home)
	if len(list) != 2 || !list[0].Current || list[0].Address != "192.0.2.10" || list[0].Agent != "Firefox at home" ||
		list[1].Current || list[1].Address != "198.51.100.20" || list[1].Connected {
		t.Fatalf("clients %+v", list)
	}
	workID := list[1].ID

	// A tab with the desktop open keeps the events socket.
	done := make(chan struct{})
	go func() {
		defer close(done)
		req := httptest.NewRequest(http.MethodGet, "http://desk.lan/api/v1/events", nil)
		req.Header.Set("Origin", "http://desk.lan")
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Connection", "Upgrade")
		req.AddCookie(&http.Cookie{Name: lt.s.cookie, Value: work})
		lt.h.ServeHTTP(httptest.NewRecorder(), req)
	}()
	<-opened
	if c := lt.clients(home)[1]; !c.Connected {
		t.Fatalf("open tab not seen: %+v", c)
	}
	close(closeEvents)
	<-done
	if c := lt.clients(home)[1]; c.Connected {
		t.Fatalf("closed tab still connected: %+v", c)
	}

	// Locked browsers cannot list (it would show where the user is), and
	// are shown locked; unlocking keeps the entry, from the new address.
	lt.expect(lt.do(http.MethodPost, "/api/auth/lock", home, ""), http.StatusNoContent, "")
	lt.expect(lt.do(http.MethodGet, "/api/auth/clients", work, ""), http.StatusLocked, "locked")
	w := lt.clientReq(http.MethodPost, "/api/auth/unlock", work, "203.0.113.30", "Chrome at work", `{"password":"secret"}`)
	lt.expect(w, http.StatusOK, "")
	work = cookie(w, lt.s.cookie).Value
	list = lt.clients(work)
	if len(list) != 2 || list[0].ID != workID || list[0].Address != "203.0.113.30" || !list[1].Locked {
		t.Fatalf("after unlock %+v", list)
	}

	// Signing another browser out; not this one, which logs out instead.
	lt.expect(lt.do(http.MethodPost, "/api/auth/clients/signout", work, `{"id":"`+workID+`"}`), http.StatusBadRequest, "bad_request")
	homeID := list[1].ID
	lt.expect(lt.do(http.MethodPost, "/api/auth/clients/signout", home, `{"id":"`+workID+`"}`), http.StatusLocked, "locked")
	lt.expect(lt.do(http.MethodPost, "/api/auth/clients/signout", work, `{"id":"`+homeID+`"}`), http.StatusNoContent, "")
	lt.expect(lt.do(http.MethodGet, "/api/auth/session", home, ""), http.StatusUnauthorized, "not_logged_in")
	lt.expect(lt.do(http.MethodPost, "/api/auth/clients/signout", work, `{"id":"`+homeID+`"}`), http.StatusNotFound, "not_found")
	if r := rec.last(); r.Event != "signout_client" || r.message() != "alice signed out the browser at 192.0.2.10 from 192.0.2.1" {
		t.Fatalf("audit %+v %q", r, r.message())
	}
	if list := lt.clients(work); len(list) != 1 {
		t.Fatalf("after signing out: %+v", list)
	}
}

// TestClientsOnlyTheUsers: another user's browsers are neither listed nor
// signed out.
func TestClientsOnlyTheUsers(t *testing.T) {
	lt := newLockTest(t, noContent)
	lt.addSession("bob", noContent)
	alice := lt.login("alice")
	bob, _ := lt.s.tokens.NewFor("bob", clientInfo{addr: "192.0.2.99"})
	if list := lt.clients(alice); len(list) != 1 || !list[0].Current {
		t.Fatalf("alice sees %+v", list)
	}
	bobID := lt.clients(bob)[0].ID
	lt.expect(lt.do(http.MethodPost, "/api/auth/clients/signout", alice, `{"id":"`+bobID+`"}`), http.StatusNotFound, "not_found")
	lt.expect(lt.do(http.MethodGet, "/api/auth/session", bob, ""), http.StatusOK, "")
}
