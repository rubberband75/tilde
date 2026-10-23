// Package pamauth wraps the PAM calls the session helper makes.
//
// The real implementation (pam_linux.go) needs cgo and libpam headers
// (Debian/Ubuntu/Mint: apt install libpam0g-dev). Builds with CGO_ENABLED=0
// or the "nopam" tag get a stub that refuses every login, so the rest of the
// code can still be compiled and tested anywhere.
package pamauth

import "errors"

// Service is the PAM service name, i.e. the file /etc/pam.d/tilde.
const Service = "tilde"

// Errors returned by Authenticate. Anything else is an internal error.
var (
	ErrAuthFailed      = errors.New("authentication failed")
	ErrPasswordExpired = errors.New("password expired; change it on the machine first")
	ErrAccountExpired  = errors.New("account expired or not allowed to log in")
	ErrUnavailable     = errors.New("tilde was built without PAM support")
)

// Conversation answers what PAM asks while it authenticates. The password
// is one answer; two-factor modules ask more (D43).
type Conversation interface {
	// Prompt answers a question; echo says the answer may be shown.
	Prompt(text string, echo bool) (string, error)
	// Message passes on an informational or error message.
	Message(text string, isError bool)
}

// Session is an authenticated PAM transaction. Call Open to start a login
// session, and always call Close when finished.
type Session interface {
	// Open sets env in the PAM environment, establishes credentials and
	// opens the session. It returns the resulting PAM environment (which
	// pam_systemd fills with XDG_RUNTIME_DIR, XDG_SESSION_ID, ...).
	//
	// pam_systemd registers the session for the *calling process*, which is
	// why this runs in the per-login helper and never in the main server.
	Open(env map[string]string) (map[string]string, error)
	// Close closes the session (if opened), deletes credentials and ends
	// the transaction. It is safe to call more than once.
	Close() error
}
