// Package stdio protects the helper's and worker's stdout, which carries the
// binary yamux stream, from stray writes.
package stdio

import (
	"fmt"
	"io"
	"os"
	"syscall"
)

// ProtectStdout duplicates fd 1 into a new file that only the protocol code
// writes to, then points fd 1 at stderr. After this call, anything that
// prints to stdout (a stray fmt.Println, a PAM module, a C library) lands in
// the log instead of corrupting the stream.
func ProtectStdout() (*os.File, error) {
	fd, err := syscall.Dup(1)
	if err != nil {
		return nil, fmt.Errorf("dup stdout: %w", err)
	}
	syscall.CloseOnExec(fd)
	if err := syscall.Dup3(2, 1, 0); err != nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("redirect stdout to stderr: %w", err)
	}
	return os.NewFile(uintptr(fd), "protocol-out"), nil
}

// Conn joins a reader and a writer into the io.ReadWriteCloser yamux needs.
type Conn struct {
	io.Reader
	io.Writer
	Closers []io.Closer
}

// Close closes every closer and returns the first error.
func (c *Conn) Close() error {
	var first error
	for _, cl := range c.Closers {
		if err := cl.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}
