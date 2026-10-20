//go:build !linux || !cgo || nopam

package pamauth

// Available reports whether this build can talk to PAM.
const Available = false

// Authenticate always fails in builds without PAM.
func Authenticate(user, password, remoteAddr string) (Session, error) {
	return nil, ErrUnavailable
}
