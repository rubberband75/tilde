package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// eventsConn is a client on the events socket that collects "fs" events.
type eventsConn struct {
	t    *testing.T
	conn *websocket.Conn
	fs   chan []string
}

func dialEvents(t *testing.T, c *http.Client) *eventsConn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://worker/api/v1/events", &websocket.DialOptions{HTTPClient: c})
	if err != nil {
		t.Fatal(err)
	}
	ec := &eventsConn{t: t, conn: conn, fs: make(chan []string, 64)}
	t.Cleanup(func() { conn.CloseNow() })
	go func() {
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil {
				close(ec.fs)
				return
			}
			var ev event
			if json.Unmarshal(data, &ev) == nil && ev.Type == "fs" {
				ec.fs <- ev.Dirs
			}
		}
	}()
	return ec
}

func (ec *eventsConn) watch(dirs ...string) {
	ec.t.Helper()
	b, _ := json.Marshal(clientMessage{Type: "watch", Dirs: dirs})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ec.conn.Write(ctx, websocket.MessageText, b); err != nil {
		ec.t.Fatal(err)
	}
	// The worker applies the message asynchronously; give it a moment
	// before the test changes files.
	time.Sleep(100 * time.Millisecond)
}

// expect waits for an "fs" event that names all of dirs.
func (ec *eventsConn) expect(dirs ...string) {
	ec.t.Helper()
	timeout := time.After(5 * time.Second)
	seen := map[string]bool{}
	for {
		select {
		case got, ok := <-ec.fs:
			if !ok {
				ec.t.Fatal("events socket closed")
			}
			for _, d := range got {
				seen[d] = true
			}
			if !slices.ContainsFunc(dirs, func(d string) bool { return !seen[d] }) {
				return
			}
		case <-timeout:
			ec.t.Fatalf("no fs event for %v (saw %v)", dirs, seen)
		}
	}
}

// drain drops events received so far.
func (ec *eventsConn) drain() {
	for {
		select {
		case <-ec.fs:
		default:
			return
		}
	}
}

// expectNone checks that no "fs" event arrives for a while.
func (ec *eventsConn) expectNone() {
	ec.t.Helper()
	select {
	case got := <-ec.fs:
		ec.t.Fatalf("unexpected fs event %v", got)
	case <-time.After(3 * watchDelay):
	}
}

func TestWatchFolders(t *testing.T) {
	c, home := startWorker(t)
	ec := dialEvents(t, c)
	docs := filepath.Join(home, "Documents")
	if err := os.Mkdir(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	ec.watch(home, docs, "relative/ignored", home+"/../ignored")

	// Created, written, renamed and removed entries all count.
	write(t, filepath.Join(docs, "a.txt"), "a")
	ec.expect(docs)
	if err := os.Rename(filepath.Join(docs, "a.txt"), filepath.Join(home, "a.txt")); err != nil {
		t.Fatal(err)
	}
	ec.expect(docs, home)
	if err := os.Chmod(filepath.Join(home, "a.txt"), 0o600); err != nil {
		t.Fatal(err)
	}
	ec.expect(home)

	// Changes inside subfolders are not reported; reading is not a change.
	sub := filepath.Join(docs, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	ec.expect(docs)
	write(t, filepath.Join(sub, "deep.txt"), "x")
	if _, err := os.ReadDir(docs); err != nil {
		t.Fatal(err)
	}
	read(t, filepath.Join(home, "a.txt"))
	ec.expectNone()

	// Unwatched folders are quiet.
	ec.watch(home)
	write(t, filepath.Join(docs, "b.txt"), "b")
	ec.expectNone()

	// A watched folder that goes away is reported. Once it is back, the
	// client's next watch message (even an unchanged one) watches it again.
	ec.watch(home, docs)
	if err := os.RemoveAll(docs); err != nil {
		t.Fatal(err)
	}
	ec.expect(docs, home)
	if err := os.Mkdir(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	ec.expect(home)
	ec.watch(home, docs)
	write(t, filepath.Join(docs, "c.txt"), "c")
	ec.expect(docs)
}

func TestWatchSharedAndLinkedFolders(t *testing.T) {
	c, home := startWorker(t)
	real := filepath.Join(home, "real")
	link := filepath.Join(home, "link")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	// Two clients, two names for one folder: one inotify watch, both names
	// reported, and the watch outlives the first client to let go.
	a := dialEvents(t, c)
	b := dialEvents(t, c)
	a.watch(real)
	b.watch(link)
	write(t, filepath.Join(real, "1"), "1")
	a.expect(real, link)
	b.expect(real, link)

	a.watch()
	write(t, filepath.Join(real, "2"), "2")
	b.expect(link)

	// A client that disconnects gives up its watches. (Events go to every
	// client, so a saw the last one through b's watch.)
	b.conn.Close(websocket.StatusNormalClosure, "")
	a.watch(home)
	a.drain()
	write(t, filepath.Join(real, "3"), "3")
	a.expectNone()
}
