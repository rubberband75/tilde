package worker

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tilde/internal/protocol"
)

func TestParseProcNet(t *testing.T) {
	in := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 3500007F:0035 00000000:0000 0A 00000000:00000000 00:00000000 00000000   991        0 18778 1 0000000000000000 100 0 0 10 5
   1: 00000000:22B8 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 13665 1 0000000000000000 100 0 0 10 0
   2: 0100007F:22B8 0100007F:C350 01 00000000:00000000 00:00000000 00000000  1000        0 13666 1 0000000000000000 100 0 0 10 0
   0: 00000000000000000000000001000000:08AE 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 21743 1 0000000000000000 100 0 0 10 0
garbage`
	var got []listenSocket
	for _, s := range parseProcNet(strings.NewReader(in)) {
		if s.listen {
			got = append(got, s)
		}
	}
	want := []string{"127.0.0.53:53 991 18778", "0.0.0.0:8888 1000 13665", "[::1]:2222 1000 21743"}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i, s := range got {
		if g := fmt.Sprintf("%s %d %s", net.JoinHostPort(s.ip.String(), fmt.Sprint(s.port)), s.uid, s.inode); g != want[i] {
			t.Errorf("%d: %s, want %s", i, g, want[i])
		}
	}

	// Where each is reached: loopback for every address, IPv4 first.
	for _, c := range []struct {
		socks []listenSocket
		want  string
	}{
		{[]listenSocket{{ip: net.IPv6unspecified, port: 80}, {ip: net.IPv4zero.To4(), port: 80}}, "127.0.0.1:80"},
		{[]listenSocket{{ip: net.IPv6loopback, port: 80}}, "[::1]:80"},
		// Not 127.0.0.1: another user may have that.
		{[]listenSocket{{ip: net.ParseIP("127.0.0.2").To4(), port: 80}}, "127.0.0.2:80"},
		{[]listenSocket{{ip: net.ParseIP("192.0.2.7").To4(), port: 80}}, "192.0.2.7:80"},
	} {
		if got := dialAddress(c.socks); got != c.want {
			t.Errorf("dialAddress(%v) = %s, want %s", c.socks, got, c.want)
		}
	}
}

// TestLocalApps: a web server of the user's is listed with its page's
// title and process, and the server reaches it through the worker's
// internal dial; ports nobody of the user's listens on are refused.
func TestLocalApps(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "<html><head><title>  Lab &amp;\n Notes </title></head>path=%s", r.URL.Path)
	}))
	defer app.Close()
	_, portStr, _ := net.SplitHostPort(app.Listener.Addr().String())
	var port int
	fmt.Sscan(portStr, &port)

	w := driveWorker(t, nil)
	rec := serve(t, w, "GET", "/api/v1/apps", "")
	var res struct{ Apps []LocalApp }
	json.Unmarshal(rec.Body.Bytes(), &res)
	var found *LocalApp
	for i := range res.Apps {
		if res.Apps[i].Port == port {
			found = &res.Apps[i]
		}
	}
	exe, _ := os.Executable()
	if found == nil || !found.HTTP || found.Title != "Lab & Notes" || found.PID != os.Getpid() ||
		found.Process != filepath.Base(exe) || found.Address != "127.0.0.1" {
		t.Fatalf("port %d: %+v in %+v", port, found, res.Apps)
	}

	c, _ := startWorker(t)
	// A timeout wraps the body, which then cannot be written to.
	c = &http.Client{Transport: c.Transport}
	dial := func(port int, internal bool) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, api("/internal/dial", nil)+fmt.Sprintf("?port=%d", port), nil)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "tilde-tcp")
		if internal {
			req.Header.Set(protocol.InternalHeader, "1")
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp := dial(port, true)
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("dial: %d", resp.StatusCode)
	}
	conn := resp.Body.(io.ReadWriteCloser)
	defer conn.Close()
	fmt.Fprintf(conn, "GET /through HTTP/1.1\r\nHost: app\r\nConnection: close\r\n\r\n")
	got, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(got.Body)
	if !strings.Contains(string(body), "path=/through") {
		t.Fatalf("through the dial: %s", body)
	}

	// Only the server may ask; and only for the user's own ports.
	if resp := dial(port, false); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("from a browser: %d", resp.StatusCode)
	}
	free, _ := net.Listen("tcp", "127.0.0.1:0")
	freePort := free.Addr().(*net.TCPAddr).Port
	free.Close()
	if resp := dial(freePort, true); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("nothing listening: %d", resp.StatusCode)
	}
	if resp := dial(0, true); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("port 0: %d", resp.StatusCode)
	}
}

// TestDialOwnExactAddress: a listener on 127.0.0.2 is dialled there, not
// at 127.0.0.1, where another user's server may listen on the same port;
// and the connection's far end is checked to be the user's.
func TestDialOwnExactAddress(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.2:0")
	if err != nil {
		t.Skip("cannot listen on 127.0.0.2:", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Write([]byte("hi"))
			c.Close()
		}
	}()
	conn, err := dialOwn(t.Context(), ln.Addr().(*net.TCPAddr).Port)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if got := conn.RemoteAddr().(*net.TCPAddr).IP.String(); got != "127.0.0.2" {
		t.Fatalf("dialled %s", got)
	}
	l, r := conn.LocalAddr().(*net.TCPAddr).AddrPort(), conn.RemoteAddr().(*net.TCPAddr).AddrPort()
	if acceptedByUser(r, l, os.Getuid()+1) {
		t.Fatal("the far end counted as another user's")
	}
}
