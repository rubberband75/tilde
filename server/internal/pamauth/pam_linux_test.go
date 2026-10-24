//go:build linux && cgo && !nopam

package pamauth

import (
	"errors"
	"testing"

	"github.com/msteinert/pam/v2"
)

// TestAuthStageSaysNothing: before the password is known to be right, a
// refusal never tells the account exists or what is wrong with it.
func TestAuthStageSaysNothing(t *testing.T) {
	for _, c := range []struct {
		err        error
		auth, acct error
	}{
		{pam.ErrPermDenied, ErrAuthFailed, ErrAccountExpired},
		{pam.ErrAcctExpired, ErrAuthFailed, ErrAccountExpired},
		{pam.ErrNewAuthtokReqd, ErrAuthFailed, ErrPasswordExpired},
		{pam.ErrUserUnknown, ErrAuthFailed, ErrAuthFailed},
		{pam.ErrAuth, ErrAuthFailed, ErrAuthFailed},
	} {
		if got := authStageErr(c.err); got != c.auth {
			t.Errorf("auth stack %v: %v, want %v", c.err, got, c.auth)
		}
		if got := classify(c.err); got != c.acct {
			t.Errorf("account stack %v: %v, want %v", c.err, got, c.acct)
		}
	}
	if got := authStageErr(pam.ErrSystem); errors.Is(got, ErrAuthFailed) || got == nil {
		t.Errorf("a system error became %v", got)
	}
}
