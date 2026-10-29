package worker

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// The events WebSocket (GET /api/v1/events) is the control channel D11
// planned: the worker pushes changes instead of the browser polling. It
// carries job progress and changes to the folders the client watches.

// event is one JSON text frame on the events socket.
type event struct {
	Type string `json:"type"` // "snapshot", "job", "job-removed", "fs", "drives", "network", "polkit", "settings", "notifications"
	// snapshot: every job, sent first on each connection.
	Jobs []JobInfo `json:"jobs,omitempty"`
	// job: the whole changed job.
	Job *JobInfo `json:"job,omitempty"`
	// job-removed: the id of a dismissed job.
	ID string `json:"id,omitempty"`
	// fs: watched folders whose contents changed.
	Dirs []string `json:"dirs,omitempty"`
}

// clientMessage is a frame from the client: {"type":"watch","dirs":[...]}
// replaces the set of folders it wants "fs" events for, and
// {"type":"resources","on":true} asks for resource snapshots (D61).
type clientMessage struct {
	Type string   `json:"type"`
	Dirs []string `json:"dirs"`
	On   bool     `json:"on"`
}

// eventBuffer is how many events a client may fall behind before it is
// dropped; it reconnects and gets a fresh snapshot.
const eventBuffer = 256

type eventHub struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

func newEventHub() *eventHub {
	return &eventHub{subs: map[chan []byte]struct{}{}}
}

func (h *eventHub) publish(ev event) {
	b, err := json.Marshal(ev)
	if err != nil {
		slog.Error("worker: encode event", "err", err)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- b:
		default:
			delete(h.subs, ch)
			close(ch)
		}
	}
}

// subscribe registers a client and returns its first message, built by
// snapshot while new events are held back, so nothing falls between the
// snapshot and the stream. Events repeat whole objects, so one that also
// made it into the snapshot does no harm.
func (h *eventHub) subscribe(snapshot func() event) ([]byte, <-chan []byte, func()) {
	ch := make(chan []byte, eventBuffer)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	first, _ := json.Marshal(snapshot())
	h.mu.Unlock()
	return first, ch, func() {
		h.mu.Lock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
}

// handleEvents streams events to one client. The Origin check happens in
// the main server before the upgrade is proxied here.
func (w *Worker) handleEvents(rw http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(rw, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer c.CloseNow()
	// Room for maxWatchesPerClient long paths.
	c.SetReadLimit(1 << 20)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// This client's resource snapshots, while it asks for them.
	res := make(chan []byte, 4)
	defer w.resources.unwatch(res)
	// The reader owns the client's watches and drops them when the
	// connection ends (the deferred CloseNow makes its Read fail).
	go func() {
		defer cancel()
		watches := &watchSet{w: w.watcher}
		defer watches.clear()
		// The only one that starts watching: stopping here too means a
		// last message during the close cannot leave the sampling running.
		defer w.resources.unwatch(res)
		readClient(ctx, c, watches, func(on bool) {
			if on {
				w.resources.watch(res)
			} else {
				w.resources.unwatch(res)
			}
		})
	}()

	first, ch, unsubscribe := w.events.subscribe(func() event {
		return event{Type: "snapshot", Jobs: w.jobs.list()}
	})
	defer unsubscribe()
	if err := writeEvent(ctx, c, first); err != nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case b, ok := <-ch:
			if !ok {
				c.Close(websocket.StatusTryAgainLater, "too slow")
				return
			}
			if err := writeEvent(ctx, c, b); err != nil {
				return
			}
		case b := <-res:
			if err := writeEvent(ctx, c, b); err != nil {
				return
			}
		}
	}
}

// readClient applies the client's watch messages until it disconnects.
func readClient(ctx context.Context, c *websocket.Conn, watches *watchSet, resources func(on bool)) {
	for {
		typ, b, err := c.Read(ctx)
		if err != nil {
			return
		}
		var m clientMessage
		if typ != websocket.MessageText || json.Unmarshal(b, &m) != nil {
			continue
		}
		switch m.Type {
		case "watch":
			watches.set(m.Dirs)
		case "resources":
			resources(m.On)
		}
	}
}

func writeEvent(ctx context.Context, c *websocket.Conn, b []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.Write(ctx, websocket.MessageText, b)
}
