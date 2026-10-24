package worker

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"

	"tilde/internal/protocol"
)

// The worker's part of the audit log (D46): what the user does through
// Tilde (terminals, deletions, mounts, polkit) goes to the server on the
// session stream, and root writes it to the journal. The worker cannot
// write the record itself: as the user's process, it could be made to
// write anything, and records under the user's own uid prove little.

const (
	// auditQueueSize events wait while the session stream reconnects;
	// more are counted and dropped.
	auditQueueSize = 256
	// maxAuditPaths and maxAuditBytes keep an event well inside the
	// server's 4 KiB stream lines: the first paths, and how many.
	maxAuditPaths = 20
	maxAuditBytes = 2500
)

type auditQueue struct {
	ch      chan protocol.AuditEvent
	dropped atomic.Int64
}

func newAuditQueue() *auditQueue {
	return &auditQueue{ch: make(chan protocol.AuditEvent, auditQueueSize)}
}

// audit reports ev, made by request r (nil for what no request caused).
func (w *Worker) audit(r *http.Request, ev protocol.AuditEvent) {
	if r != nil {
		ev.Rhost = r.Header.Get(protocol.ClientHeader)
	}
	w.auditQ.add(ev)
}

func (q *auditQueue) add(ev protocol.AuditEvent) {
	select {
	case q.ch <- ev:
	default:
		q.dropped.Add(1)
	}
}

// next takes the dropped count along with an event.
func (q *auditQueue) next(ev protocol.AuditEvent) protocol.AuditEvent {
	ev.Dropped += int(q.dropped.Swap(0))
	return ev
}

// auditPaths are the first of paths that fit an event, long ones cut
// short, and how many there were. Sizes count as JSON encodes them: a
// control character takes six bytes there.
func auditPaths(paths []string) ([]string, int) {
	var out []string
	size := 0
	for _, p := range paths {
		// Room for at least a cut-short path ("…" alone takes 5 bytes).
		if len(out) == maxAuditPaths || maxAuditBytes-size < 64 {
			break
		}
		for jsonLen(p) > maxAuditBytes-size {
			p = strings.ToValidUTF8(p[:len(p)*3/4], "") + "…"
		}
		out = append(out, p)
		size += jsonLen(p)
	}
	return out, len(paths)
}

func jsonLen(s string) int {
	b, _ := json.Marshal(s)
	return len(b)
}
