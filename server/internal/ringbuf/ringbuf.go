// Package ringbuf provides a fixed-capacity byte buffer that keeps the most
// recent bytes written to it. Terminals use it to replay recent output when a
// browser reattaches.
//
// A Buffer is not safe for concurrent use; callers hold their own lock.
package ringbuf

// Buffer keeps the last Cap() bytes written to it.
type Buffer struct {
	buf   []byte
	start int // index of the oldest byte
	n     int // number of valid bytes
}

// New returns a Buffer that holds at most capacity bytes.
func New(capacity int) *Buffer {
	if capacity < 0 {
		capacity = 0
	}
	return &Buffer{buf: make([]byte, capacity)}
}

// Cap returns the buffer's capacity in bytes.
func (b *Buffer) Cap() int { return len(b.buf) }

// Len returns the number of bytes currently held.
func (b *Buffer) Len() int { return b.n }

// Write appends p, discarding the oldest bytes when the buffer is full.
// It never fails and always reports len(p) bytes written.
func (b *Buffer) Write(p []byte) (int, error) {
	total := len(p)
	c := len(b.buf)
	if c == 0 || total == 0 {
		return total, nil
	}
	if total >= c {
		copy(b.buf, p[total-c:])
		b.start, b.n = 0, c
		return total, nil
	}
	end := (b.start + b.n) % c
	k := copy(b.buf[end:], p)
	if k < total {
		copy(b.buf, p[k:])
	}
	b.n += total
	if b.n > c {
		b.start = (b.start + b.n - c) % c
		b.n = c
	}
	return total, nil
}

// Bytes returns a copy of the held bytes, oldest first.
func (b *Buffer) Bytes() []byte {
	out := make([]byte, b.n)
	if b.n == 0 {
		return out
	}
	c := len(b.buf)
	k := copy(out, b.buf[b.start:min(b.start+b.n, c)])
	if k < b.n {
		copy(out[k:], b.buf[:b.n-k])
	}
	return out
}

// Reset discards all held bytes.
func (b *Buffer) Reset() { b.start, b.n = 0, 0 }
