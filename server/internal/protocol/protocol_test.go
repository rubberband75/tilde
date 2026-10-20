package protocol

import (
	"bufio"
	"bytes"
	"io"
	"testing"
)

func TestReadLineUnbufferedLeavesTheRest(t *testing.T) {
	var buf bytes.Buffer
	WriteLine(&buf, HelperRequest{Mode: ModeSession, User: "chandler"})
	buf.WriteString("YAMUX-BYTES")

	var req HelperRequest
	if err := ReadLineUnbuffered(&buf, &req); err != nil {
		t.Fatal(err)
	}
	if req.User != "chandler" || req.Mode != ModeSession {
		t.Fatalf("req = %+v", req)
	}
	rest, _ := io.ReadAll(&buf)
	if string(rest) != "YAMUX-BYTES" {
		t.Fatalf("consumed too much: rest = %q", rest)
	}
}

func TestReadLineBuffered(t *testing.T) {
	var buf bytes.Buffer
	WriteLine(&buf, WorkerHello{Protocol: Version, User: "chandler"})
	buf.WriteString("after")
	br := bufio.NewReader(&buf)
	var hello WorkerHello
	if err := ReadLine(br, &hello); err != nil {
		t.Fatal(err)
	}
	rest, _ := io.ReadAll(br)
	if hello.User != "chandler" || string(rest) != "after" {
		t.Fatalf("hello = %+v, rest = %q", hello, rest)
	}
}
