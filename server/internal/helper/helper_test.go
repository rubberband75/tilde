package helper

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"testing"

	"tilde/internal/protocol"
)

// TestConversation: the password from the request answers PAM's first
// hidden prompt, once; every other question goes to the server, with PAM's
// messages, and the answer is read without taking bytes meant for the
// worker (D43).
func TestConversation(t *testing.T) {
	var out bytes.Buffer
	in := strings.NewReader(`{"answer":"123456"}` + "\n" + "WORKER-BYTES")
	c := &conversation{user: "alice", password: "secret", proto: &out, in: in}

	if a, err := c.Prompt("login: ", true); err != nil || a != "alice" {
		t.Fatalf("login prompt: %q, %v", a, err)
	}
	if a, err := c.Prompt("Password: ", false); err != nil || a != "secret" {
		t.Fatalf("password prompt: %q, %v", a, err)
	}
	if out.Len() != 0 {
		t.Fatalf("the password step went to the server: %q", out.String())
	}

	c.Message("Duo push sent.", false)
	if a, err := c.Prompt("Verification code: ", false); err != nil || a != "123456" {
		t.Fatalf("code prompt: %q, %v", a, err)
	}
	var res protocol.HelperResult
	if err := protocol.ReadLine(bufio.NewReader(&out), &res); err != nil || res.OK || res.Prompt == nil {
		t.Fatalf("sent %q: %v", out.String(), err)
	}
	if p := *res.Prompt; p.Text != "Verification code:" || p.Echo || len(p.Info) != 1 || p.Info[0] != "Duo push sent." {
		t.Fatalf("prompt %+v", p)
	}
	if rest, _ := io.ReadAll(in); string(rest) != "WORKER-BYTES" {
		t.Fatalf("read past the answer: rest %q", rest)
	}

	// A second password prompt is not answered with the password again.
	if _, err := c.Prompt("Password: ", false); err == nil {
		t.Fatal("answered a second hidden prompt without asking")
	}
}
