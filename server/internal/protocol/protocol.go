// Package protocol defines the messages exchanged between the main server,
// the root session helper and the per-user worker before the yamux stream
// multiplexer takes over the pipe.
//
// Startup sequence on the helper's stdin/stdout pipe:
//
//  1. server → helper : HelperRequest (one JSON line)
//  2. helper → server : HelperResult  (one JSON line)
//  3. (session mode only) the helper execs the worker with the same pipe
//  4. worker → server : WorkerHello   (one JSON line)
//  5. both sides switch to yamux: the server is the yamux client and opens
//     one stream per HTTP request; the worker is the yamux server.
//
// Anything else written to stdout corrupts the stream, so both the helper and
// the worker move fd 1 aside at startup (see package stdio).
package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Version is bumped whenever the server/worker contract changes in a way
// that is not backwards compatible. The server refuses workers that report a
// different version (this matters once workers run on remote hosts over SSH).
const Version = 1

// Helper modes.
const (
	// ModeSession authenticates, opens a PAM session and starts the worker.
	ModeSession = "session"
	// ModeVerify only checks the password (used for logins to an existing
	// session and for unlocking a locked client).
	ModeVerify = "verify"
)

// HelperRequest is the first and only message the server sends to a helper.
// It carries the password, so it must never be logged.
type HelperRequest struct {
	Mode       string `json:"mode"`
	User       string `json:"user"`
	Password   string `json:"password"`
	RemoteAddr string `json:"remoteAddr,omitempty"`
}

// HelperResult reports whether authentication (and session setup) worked.
type HelperResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// Code is a stable machine-readable error, e.g. "auth_failed".
	Code string `json:"code,omitempty"`
	UID  int    `json:"uid,omitempty"`
}

// WorkerHello is the worker's first message once it is running as the user.
type WorkerHello struct {
	Protocol int    `json:"protocol"`
	Version  string `json:"version"`
	User     string `json:"user"`
	UID      int    `json:"uid"`
	Home     string `json:"home"`
}

// InternalHeader marks requests the server makes to the worker for itself,
// on routes under /internal/ (the idle lock delay, D40). Browsers never
// reach those routes: the server proxies only /api/v1/*, and its proxy
// deletes this header from everything it forwards.
const InternalHeader = "X-Tilde-Internal"

// Error codes shared between helper, server and frontend.
const (
	CodeAuthFailed      = "auth_failed"
	CodeAccountExpired  = "account_expired"
	CodePasswordExpired = "password_expired"
	CodeRootDisabled    = "root_login_disabled"
	CodeNotAllowed      = "not_allowed"
	CodeInternal        = "internal_error"
)

const maxLine = 64 * 1024

// ErrLineTooLong is returned when a control line exceeds 64 KiB.
var ErrLineTooLong = errors.New("protocol: line too long")

// WriteLine writes v as a single JSON line.
func WriteLine(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}

// ReadLineUnbuffered reads one JSON line into v one byte at a time, so that
// no bytes after the newline are consumed. Use it wherever the rest of the
// stream must stay untouched for another reader (the helper hands its stdin
// to the worker after reading the request).
func ReadLineUnbuffered(r io.Reader, v any) error {
	var line []byte
	b := make([]byte, 1)
	for {
		n, err := r.Read(b)
		if n == 1 {
			if b[0] == '\n' {
				break
			}
			line = append(line, b[0])
			if len(line) > maxLine {
				return ErrLineTooLong
			}
		}
		if err != nil {
			if err == io.EOF && len(line) > 0 {
				break
			}
			return err
		}
	}
	if err := json.Unmarshal(line, v); err != nil {
		return fmt.Errorf("protocol: decode line: %w", err)
	}
	return nil
}

// LineReader is satisfied by *bufio.Reader.
type LineReader interface {
	ReadSlice(delim byte) ([]byte, error)
}

// ReadLine reads one JSON line from a buffered reader into v. The buffered
// reader keeps any bytes after the newline for the next consumer.
func ReadLine(r LineReader, v any) error {
	line, err := r.ReadSlice('\n')
	if err != nil {
		return err
	}
	if len(line) > maxLine {
		return ErrLineTooLong
	}
	if err := json.Unmarshal(line, v); err != nil {
		return fmt.Errorf("protocol: decode line: %w", err)
	}
	return nil
}
