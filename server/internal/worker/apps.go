package worker

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Local web apps (D66, D76): the user's programs listening on TCP ports
// (code-server, Jupyter, a development server), which Tilde opens in a
// window from a second port of its own. The server never connects to
// them: it asks the worker, which dials as the user and only ports the
// user's own sockets listen on, so another user's app is not reachable
// through Tilde.

// LocalApp is a TCP port one of the user's programs listens on.
type LocalApp struct {
	Port int `json:"port"`
	// Address it listens on: 127.0.0.1, ::1, 0.0.0.0, :: or another.
	Address string `json:"address"`
	Process string `json:"process,omitempty"`
	PID     int    `json:"pid,omitempty"`
	// HTTP: it answered a request; Title is its page's <title>.
	HTTP  bool   `json:"http"`
	Title string `json:"title,omitempty"`
}

// listenSocket is a socket from /proc/net/tcp or tcp6: a listening one,
// or (for dialOwn's check) a connection, with its remote end.
type listenSocket struct {
	ip     net.IP
	port   int
	uid    int
	inode  string
	listen bool
	remote netip.AddrPort
}

// procNetFiles are read for listening sockets; tests replace them.
var procNetFiles = []string{"/proc/net/tcp", "/proc/net/tcp6"}

// parseProcNet reads /proc/net/tcp's format: hex addresses (IPv4 as one
// little-endian word, IPv6 as four), state 0A for LISTEN, the owner's uid.
func parseProcNet(r io.Reader) []listenSocket {
	var out []listenSocket
	sc := bufio.NewScanner(r)
	sc.Scan() // the header
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 10 {
			continue
		}
		ip, port, ok1 := procAddr(f[1])
		rip, rport, ok2 := procAddr(f[2])
		uid, err := strconv.Atoi(f[7])
		if !ok1 || !ok2 || err != nil {
			continue
		}
		raddr, _ := netip.AddrFromSlice(rip)
		out = append(out, listenSocket{ip: ip, port: port, uid: uid, inode: f[9], listen: f[3] == "0A",
			remote: netip.AddrPortFrom(raddr.Unmap(), uint16(rport))})
	}
	return out
}

func procAddr(s string) (net.IP, int, bool) {
	addr, portHex, ok := strings.Cut(s, ":")
	if !ok {
		return nil, 0, false
	}
	port, err1 := strconv.ParseUint(portHex, 16, 16)
	raw, err2 := hex.DecodeString(addr)
	if err1 != nil || err2 != nil || (len(raw) != 4 && len(raw) != 16) {
		return nil, 0, false
	}
	// Each 32-bit word is in host (little-endian) order.
	ip := make(net.IP, len(raw))
	for i := 0; i < len(raw); i += 4 {
		ip[i], ip[i+1], ip[i+2], ip[i+3] = raw[i+3], raw[i+2], raw[i+1], raw[i]
	}
	return ip, int(port), true
}

func procSockets() []listenSocket {
	var out []listenSocket
	for _, file := range procNetFiles {
		f, err := os.Open(file)
		if err != nil {
			continue
		}
		out = append(out, parseProcNet(f)...)
		f.Close()
	}
	return out
}

// ownListeners returns the sockets listening for this user, by port.
func ownListeners() map[int][]listenSocket {
	uid := os.Getuid()
	out := map[int][]listenSocket{}
	for _, s := range procSockets() {
		if s.listen && s.uid == uid {
			out[s.port] = append(out[s.port], s)
		}
	}
	return out
}

// dialAddress is where to reach a port: the address it listens on, or
// loopback for one listening on every address (IPv4 first). Any other
// address, 127.0.0.2 included, is dialled as it is: another user may
// listen on the same port at 127.0.0.1.
func dialAddress(socks []listenSocket) string {
	port := strconv.Itoa(socks[0].port)
	for _, v4 := range []bool{true, false} {
		for _, s := range socks {
			if (s.ip.To4() != nil) == v4 && s.ip.IsUnspecified() {
				if v4 {
					return net.JoinHostPort("127.0.0.1", port)
				}
				return net.JoinHostPort("::1", port)
			}
		}
	}
	return net.JoinHostPort(socks[0].ip.String(), port)
}

// socketOwners maps socket inodes to the user's processes that hold them,
// from /proc/*/fd; other users' processes cannot be read, and need not be.
func socketOwners(inodes map[string]bool) map[string]int {
	out := map[string]int{}
	procs, _ := os.ReadDir("/proc")
	uid := os.Getuid()
	for _, p := range procs {
		pid, err := strconv.Atoi(p.Name())
		if err != nil || len(out) == len(inodes) {
			continue
		}
		if info, err := p.Info(); err != nil || !ownedBy(info, uid) {
			continue
		}
		fds, err := os.ReadDir(filepath.Join("/proc", p.Name(), "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join("/proc", p.Name(), "fd", fd.Name()))
			if inode, ok := strings.CutPrefix(link, "socket:["); err == nil && ok {
				inode = strings.TrimSuffix(inode, "]")
				if inodes[inode] {
					if _, seen := out[inode]; !seen {
						out[inode] = pid
					}
				}
			}
		}
	}
	return out
}

// ownedBy: /proc/<pid> belongs to the process's user.
func ownedBy(info fs.FileInfo, uid int) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == uid
}

func processName(pid int) string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil || len(b) == 0 {
		b, _ = os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
		return strings.TrimSpace(string(b))
	}
	args := strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
	// "python3 -m http.server" or "node .../code-server": the program, and
	// the script it runs when that is what names it.
	name := filepath.Base(args[0])
	if len(args) > 1 && (strings.HasPrefix(name, "python") || name == "node" || name == "ruby" || name == "perl") {
		if args[1] == "-m" && len(args) > 2 {
			return name + " -m " + args[2]
		}
		if !strings.HasPrefix(args[1], "-") {
			return name + " " + filepath.Base(args[1])
		}
	}
	return name
}

var titleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// probeHTTP asks the port for its first page, briefly: whether it speaks
// HTTP, and the page's title.
func probeHTTP(ctx context.Context, addr string) (bool, string) {
	client := &http.Client{
		Timeout:       time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		// Straight to the port, never through $HTTP_PROXY, and not kept.
		Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/", nil)
	if err != nil {
		return false, ""
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, ""
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if m := titleRe.FindSubmatch(body); m != nil {
		return true, clip(strings.Join(strings.Fields(html.UnescapeString(string(m[1]))), " "), 120)
	}
	return true, ""
}

// maxProbes bounds the ports asked at once.
const maxProbes = 32

// listApps lists the user's listening ports, smallest first.
func listApps(ctx context.Context) []LocalApp {
	socks := ownListeners()
	inodes := map[string]bool{}
	for _, ss := range socks {
		for _, s := range ss {
			inodes[s.inode] = true
		}
	}
	owners := socketOwners(inodes)
	apps := make([]LocalApp, 0, len(socks))
	for port, ss := range socks {
		a := LocalApp{Port: port, Address: ss[0].ip.String()}
		for _, s := range ss {
			if pid, ok := owners[s.inode]; ok {
				a.PID, a.Process = pid, processName(pid)
				break
			}
		}
		apps = append(apps, a)
	}
	slices.SortFunc(apps, func(a, b LocalApp) int { return a.Port - b.Port })
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxProbes)
	for i := range apps {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			apps[i].HTTP, apps[i].Title = probeHTTP(ctx, dialAddress(socks[apps[i].Port]))
		}()
	}
	wg.Wait()
	return apps
}

func (w *Worker) handleApps(rw http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	writeJSON(rw, http.StatusOK, map[string]any{"apps": listApps(ctx)})
}

// errNotListening: no socket of the user's listens on the port.
var errNotListening = errors.New("none of your programs listens on that port")

// dialOwn connects to a port one of the user's sockets listens on.
//
// Between looking and dialling, the user's program may stop and another
// user's take the port; so once connected, the far end must be a socket of
// the user's (an accepted connection belongs to its listener's owner).
func dialOwn(ctx context.Context, port int) (net.Conn, error) {
	socks := ownListeners()[port]
	if len(socks) == 0 {
		return nil, errNotListening
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", dialAddress(socks))
	if err != nil {
		return nil, err
	}
	local, ok1 := conn.LocalAddr().(*net.TCPAddr)
	remote, ok2 := conn.RemoteAddr().(*net.TCPAddr)
	if !ok1 || !ok2 || !acceptedByUser(remote.AddrPort(), local.AddrPort(), os.Getuid()) {
		conn.Close()
		return nil, errNotListening
	}
	return conn, nil
}

// acceptedByUser reports whether the socket at local, connected to remote,
// is uid's. Right after connecting, the far end may not be listed yet:
// it is looked for a few times.
func acceptedByUser(local, remote netip.AddrPort, uid int) bool {
	norm := func(a netip.AddrPort) netip.AddrPort { return netip.AddrPortFrom(a.Addr().Unmap(), a.Port()) }
	local, remote = norm(local), norm(remote)
	for try := 0; try < 10; try++ {
		for _, s := range procSockets() {
			a, _ := netip.AddrFromSlice(s.ip)
			if !s.listen && netip.AddrPortFrom(a.Unmap(), uint16(s.port)) == local && s.remote == remote {
				if s.uid != uid {
					slog.Info("worker: the app's end of the connection is another user's", "local", local, "uid", s.uid)
				}
				return s.uid == uid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	slog.Info("worker: the app's end of the connection is not listed", "local", local, "remote", remote)
	return false
}

// handleDial is the server's way to a local web app (D76): an upgraded
// request whose connection then carries the app's bytes both ways. Only
// the server may ask, for a port of the user's own.
func (w *Worker) handleDial(rw http.ResponseWriter, r *http.Request) {
	if !internalOnly(rw, r) {
		return
	}
	var app net.Conn
	var err error
	if r.URL.Query().Get("xpra") == "1" {
		// The user's Xpra session (D78); never started by a connection.
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		app, err = w.xpra.dial(ctx)
		cancel()
		if errors.Is(err, errXpraNotRunning) {
			writeJSON(rw, http.StatusNotFound, apiError{Error: err.Error(), Code: "not_found"})
			return
		}
	} else {
		port, perr := strconv.Atoi(r.URL.Query().Get("port"))
		if perr != nil || port < 1 || port > 65535 {
			writeErr(rw, badRequest{"invalid port"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		app, err = dialOwn(ctx, port)
		cancel()
	}
	if errors.Is(err, errNotListening) {
		writeJSON(rw, http.StatusNotFound, apiError{Error: err.Error(), Code: "not_found"})
		return
	}
	if err != nil {
		slog.Info("worker: dial for the app port", "query", r.URL.RawQuery, "err", err)
		writeJSON(rw, http.StatusBadGateway, apiError{Error: err.Error(), Code: "unreachable"})
		return
	}
	hj, ok := rw.(http.Hijacker)
	if !ok {
		app.Close()
		writeErr(rw, errors.New("cannot take over the connection"))
		return
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		app.Close()
		return
	}
	if _, err := conn.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: tilde-tcp\r\nConnection: Upgrade\r\n\r\n")); err != nil {
		conn.Close()
		app.Close()
		return
	}
	go pipe(conn, buf.Reader, app)
}

// pipe copies between the server's stream and the app until either side
// ends, then closes both.
func pipe(conn net.Conn, buffered io.Reader, app net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(app, io.MultiReader(buffered, conn))
		done <- struct{}{}
	}()
	go func() {
		io.Copy(conn, app)
		done <- struct{}{}
	}()
	<-done
	conn.Close()
	app.Close()
	<-done
}
