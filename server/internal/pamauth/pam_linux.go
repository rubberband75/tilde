//go:build linux && cgo && !nopam

package pamauth

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"

	pam "github.com/msteinert/pam/v2"
)

// Available reports whether this build can talk to PAM.
const Available = true

// Authenticate runs the "auth" and "account" stacks for user, answering
// PAM's questions through conv.
func Authenticate(user, remoteAddr string, conv Conversation) (Session, error) {
	tx, err := pam.StartFunc(Service, user, func(s pam.Style, msg string) (string, error) {
		switch s {
		case pam.PromptEchoOff:
			return conv.Prompt(msg, false)
		case pam.PromptEchoOn:
			return conv.Prompt(msg, true)
		case pam.ErrorMsg:
			slog.Warn("pam message", "user", user, "msg", msg)
			conv.Message(msg, true)
			return "", nil
		case pam.TextInfo:
			slog.Info("pam info", "user", user, "msg", msg)
			conv.Message(msg, false)
			return "", nil
		}
		return "", errors.New("unsupported PAM conversation style")
	})
	if err != nil {
		return nil, fmt.Errorf("pam start: %w", err)
	}
	if remoteAddr != "" {
		_ = tx.SetItem(pam.Rhost, remoteAddr)
	}
	if err := tx.Authenticate(pam.DisallowNullAuthtok); err != nil {
		tx.End()
		return nil, classify(err)
	}
	if err := tx.AcctMgmt(pam.DisallowNullAuthtok); err != nil {
		tx.End()
		return nil, classify(err)
	}
	return &session{tx: tx}, nil
}

func classify(err error) error {
	switch {
	case errors.Is(err, pam.ErrNewAuthtokReqd), errors.Is(err, pam.ErrAuthtokExpired):
		return ErrPasswordExpired
	case errors.Is(err, pam.ErrAcctExpired), errors.Is(err, pam.ErrPermDenied):
		return ErrAccountExpired
	case errors.Is(err, pam.ErrAuth), errors.Is(err, pam.ErrUserUnknown),
		errors.Is(err, pam.ErrMaxtries), errors.Is(err, pam.ErrCredInsufficient),
		errors.Is(err, pam.ErrAuthinfoUnavail):
		return ErrAuthFailed
	}
	return fmt.Errorf("pam: %w", err)
}

type session struct {
	mu      sync.Mutex
	tx      *pam.Transaction
	credSet bool
	opened  bool
	closed  bool
}

func (s *session) Open(env map[string]string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range env {
		if err := s.tx.PutEnv(k + "=" + v); err != nil {
			return nil, fmt.Errorf("pam putenv %s: %w", k, err)
		}
	}
	// Linux-PAM documents pam_setcred(ESTABLISH) before pam_open_session.
	if err := s.tx.SetCred(pam.EstablishCred); err != nil {
		return nil, fmt.Errorf("pam setcred: %w", err)
	}
	s.credSet = true
	if err := s.tx.OpenSession(0); err != nil {
		return nil, fmt.Errorf("pam open session: %w", err)
	}
	s.opened = true
	out, err := s.tx.GetEnvList()
	if err != nil {
		return nil, fmt.Errorf("pam getenvlist: %w", err)
	}
	return out, nil
}

func (s *session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var first error
	if s.opened {
		if err := s.tx.CloseSession(0); err != nil {
			first = fmt.Errorf("pam close session: %w", err)
		}
	}
	if s.credSet {
		if err := s.tx.SetCred(pam.DeleteCred); err != nil && first == nil {
			first = fmt.Errorf("pam delete cred: %w", err)
		}
	}
	if err := s.tx.End(); err != nil && first == nil {
		first = fmt.Errorf("pam end: %w", err)
	}
	return first
}
