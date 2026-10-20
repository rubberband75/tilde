package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"tilde/internal/pty"
)

// terminalManager owns the user's running terminals. Terminals live in the
// worker, not the browser, so they survive refreshes and disconnects
// (decision 4c). Ids are stable ("t1", "t2", ...) so a window can find its
// terminal again after a reload.
type terminalManager struct {
	shell string
	home  string
	env   []string

	mu   sync.Mutex
	next int
	m    map[string]*pty.Terminal
}

// TerminalInfo is the JSON view of a terminal.
type TerminalInfo struct {
	ID      string    `json:"id"`
	Created time.Time `json:"created"`
	Cols    uint16    `json:"cols"`
	Rows    uint16    `json:"rows"`
	Exited  bool      `json:"exited"`
}

func newTerminalManager(shell, home string) *terminalManager {
	env := []string{"TERM=xterm-256color", "COLORTERM=truecolor"}
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if k == "TERM" || k == "COLORTERM" {
			continue
		}
		env = append(env, kv)
	}
	return &terminalManager{shell: shell, home: home, env: env, m: map[string]*pty.Terminal{}}
}

func (tm *terminalManager) create(cols, rows uint16, cwd string) (*pty.Terminal, error) {
	tm.mu.Lock()
	tm.next++
	id := fmt.Sprintf("t%d", tm.next)
	tm.mu.Unlock()

	dir := tm.home
	if cwd != "" {
		if st, err := os.Stat(cwd); err == nil && st.IsDir() {
			dir = cwd
		}
	}
	t, err := pty.Start(id, pty.Options{
		Shell: tm.shell,
		// A login shell reads ~/.profile, which a desktop session would
		// normally have run (and Mint's ~/.profile sources ~/.bashrc).
		Login: true,
		Dir:   dir,
		Env:   tm.env,
		Cols:  cols,
		Rows:  rows,
	})
	if err != nil {
		return nil, err
	}
	tm.mu.Lock()
	tm.m[id] = t
	tm.mu.Unlock()
	go func() {
		<-t.Done()
		tm.mu.Lock()
		delete(tm.m, id)
		tm.mu.Unlock()
	}()
	return t, nil
}

func (tm *terminalManager) get(id string) *pty.Terminal {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return tm.m[id]
}

func (tm *terminalManager) list() []TerminalInfo {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	out := make([]TerminalInfo, 0, len(tm.m))
	for _, t := range tm.m {
		out = append(out, info(t))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

func (tm *terminalManager) closeAll() {
	tm.mu.Lock()
	all := make([]*pty.Terminal, 0, len(tm.m))
	for _, t := range tm.m {
		all = append(all, t)
	}
	tm.mu.Unlock()
	var wg sync.WaitGroup
	for _, t := range all {
		wg.Add(1)
		go func() { defer wg.Done(); t.Close() }()
	}
	wg.Wait()
}

func info(t *pty.Terminal) TerminalInfo {
	cols, rows := t.Size()
	exited, _ := t.Exited()
	return TerminalInfo{ID: t.ID, Created: t.Created, Cols: cols, Rows: rows, Exited: exited}
}

func (w *Worker) handleTerminalList(rw http.ResponseWriter, r *http.Request) {
	writeJSON(rw, http.StatusOK, map[string]any{"terminals": w.terms.list()})
}

func (w *Worker) handleTerminalCreate(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		Cols uint16 `json:"cols"`
		Rows uint16 `json:"rows"`
		Cwd  string `json:"cwd"` // optional starting directory ("Open in Terminal")
	}
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &req); err != nil {
			writeErr(rw, err)
			return
		}
	}
	t, err := w.terms.create(req.Cols, req.Rows, req.Cwd)
	if err != nil {
		writeErr(rw, err)
		return
	}
	writeJSON(rw, http.StatusCreated, info(t))
}

func (w *Worker) handleTerminalDelete(rw http.ResponseWriter, r *http.Request) {
	t := w.terms.get(r.PathValue("id"))
	if t == nil {
		writeJSON(rw, http.StatusNotFound, apiError{Error: "no such terminal", Code: "not_found"})
		return
	}
	t.Close()
	rw.WriteHeader(http.StatusNoContent)
}

// controlMessage is a JSON text frame from the browser. Binary frames are
// raw keyboard input.
type controlMessage struct {
	Type string `json:"type"` // "resize"
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// handleTerminalAttach connects a WebSocket to a terminal: it replays the
// ring buffer, then streams live output. Several clients may attach to the
// same terminal at once.
//
// Origin checks happen in the main server before the request is proxied
// here; the worker is only reachable through that server.
func (w *Worker) handleTerminalAttach(rw http.ResponseWriter, r *http.Request) {
	t := w.terms.get(r.PathValue("id"))
	if t == nil {
		writeJSON(rw, http.StatusNotFound, apiError{Error: "no such terminal", Code: "not_found"})
		return
	}
	c, err := websocket.Accept(rw, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(4 << 20) // large pastes

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	replay, out, detach := t.Attach()
	defer detach()
	if len(replay) > 0 {
		if err := c.Write(ctx, websocket.MessageBinary, replay); err != nil {
			return
		}
	}

	go func() {
		defer cancel()
		first := true
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				if _, err := t.Write(data); err != nil {
					return
				}
				continue
			}
			var msg controlMessage
			if json.Unmarshal(data, &msg) != nil {
				continue
			}
			if msg.Type == "resize" {
				// The first resize after attaching forces a redraw so
				// full-screen programs repaint over the replayed output.
				if err := t.Resize(msg.Cols, msg.Rows, first); err != nil {
					slog.Warn("worker: resize", "terminal", t.ID, "err", err)
				}
				first = false
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case chunk, ok := <-out:
			if !ok {
				if exited, code := t.Exited(); exited {
					c.Close(websocket.StatusNormalClosure, fmt.Sprintf("exited:%d", code))
				} else {
					// Dropped for falling behind; the client reattaches.
					c.Close(websocket.StatusTryAgainLater, "too slow")
				}
				return
			}
			if err := c.Write(ctx, websocket.MessageBinary, chunk); err != nil {
				return
			}
		}
	}
}
