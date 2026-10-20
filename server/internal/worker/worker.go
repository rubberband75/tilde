// Package worker is the per-user process ("tilde worker").
//
// The worker runs with the logged-in user's UID, GIDs and groups, so every
// file operation, terminal and process it starts is checked by the kernel
// exactly as if the user were sitting at the machine. It never listens on a
// network port: it serves plain HTTP over yamux streams on its stdin/stdout,
// and the only way to reach it is through the authenticated main server.
//
// The worker's HTTP API is the browser's API: the server proxies /api/v1/*
// here unchanged (see docs/api.md).
package worker

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/hashicorp/yamux"

	"tilde/internal/buildinfo"
	"tilde/internal/mux"
	"tilde/internal/protocol"
	"tilde/internal/stdio"
	"tilde/internal/userinfo"
)

// Worker holds the per-user state behind the API.
type Worker struct {
	info       *userinfo.Info
	hostname   string
	configHome string
	umask      os.FileMode
	trash      *Trash
	terms      *terminalManager
}

// Options overrides locations, mainly for tests.
type Options struct {
	DataHome   string // $XDG_DATA_HOME, default ~/.local/share
	ConfigHome string // $XDG_CONFIG_HOME, default ~/.config
}

// New builds a Worker for info.
func New(info *userinfo.Info, opt Options) *Worker {
	if opt.DataHome == "" {
		opt.DataHome = xdgDir("XDG_DATA_HOME", filepath.Join(info.Home, ".local", "share"))
	}
	if opt.ConfigHome == "" {
		opt.ConfigHome = xdgDir("XDG_CONFIG_HOME", filepath.Join(info.Home, ".config"))
	}
	host, _ := os.Hostname()
	return &Worker{
		info:       info,
		hostname:   host,
		configHome: opt.ConfigHome,
		umask:      readUmask(),
		trash:      NewTrash(opt.DataHome),
		terms:      newTerminalManager(info.Shell, info.Home),
	}
}

func xdgDir(env, def string) string {
	if v := os.Getenv(env); v != "" && filepath.IsAbs(v) {
		return v
	}
	return def
}

// readUmask reads the process umask from /proc/self/status (Linux 4.7+),
// avoiding the racy umask(0)/umask(old) dance in a multi-threaded program.
func readUmask() os.FileMode {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0o022
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "Umask:"); ok {
			if n, err := strconv.ParseUint(strings.TrimSpace(v), 8, 32); err == nil {
				return os.FileMode(n)
			}
		}
	}
	return 0o022
}

// Handler returns the worker's HTTP API.
func (w *Worker) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/v1/hello", w.handleHello)
	m.HandleFunc("GET /api/v1/system", w.handleSystem)
	m.HandleFunc("GET /api/v1/places", w.handlePlaces)

	m.HandleFunc("GET /api/v1/fs/list", w.handleList)
	m.HandleFunc("GET /api/v1/fs/stat", w.handleStat)
	m.HandleFunc("GET /api/v1/fs/content", w.handleDownload)
	m.HandleFunc("PUT /api/v1/fs/content", w.handleUpload)
	m.HandleFunc("POST /api/v1/fs/mkdir", w.handleMkdir)
	m.HandleFunc("POST /api/v1/fs/rename", w.handleRename)
	m.HandleFunc("POST /api/v1/fs/trash", w.handleTrashPut)

	m.HandleFunc("GET /api/v1/trash", w.handleTrashList)
	m.HandleFunc("POST /api/v1/trash/restore", w.handleTrashRestore)
	m.HandleFunc("POST /api/v1/trash/empty", w.handleTrashEmpty)

	m.HandleFunc("GET /api/v1/terminals", w.handleTerminalList)
	m.HandleFunc("POST /api/v1/terminals", w.handleTerminalCreate)
	m.HandleFunc("DELETE /api/v1/terminals/{id}", w.handleTerminalDelete)
	m.HandleFunc("GET /api/v1/terminals/{id}/attach", w.handleTerminalAttach)

	m.HandleFunc("/", func(rw http.ResponseWriter, r *http.Request) {
		writeJSON(rw, http.StatusNotFound, apiError{Error: "no such endpoint", Code: "not_found"})
	})
	return m
}

func (w *Worker) handleHello(rw http.ResponseWriter, r *http.Request) {
	writeJSON(rw, http.StatusOK, map[string]any{
		"user":     w.info.Name,
		"uid":      w.info.UID,
		"home":     w.info.Home,
		"shell":    w.info.Shell,
		"hostname": w.hostname,
		"version":  buildinfo.Version,
		"protocol": protocol.Version,
	})
}

// Shutdown hangs up all terminals, as logging out does.
func (w *Worker) Shutdown() { w.terms.closeAll() }

// Serve runs the API on conn until the yamux session ends.
func Serve(ctx context.Context, conn io.ReadWriteCloser, h http.Handler) error {
	sess, err := yamux.Server(conn, mux.Config())
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 30 * time.Second,
		ErrorLog:          log.New(os.Stderr, "worker http: ", 0),
	}
	go func() {
		<-ctx.Done()
		sess.Close()
	}()
	err = srv.Serve(sess)
	if errors.Is(err, yamux.ErrSessionShutdown) || errors.Is(err, io.EOF) || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Run is the entry point for "tilde worker". It returns the exit code.
func Run() int {
	proto, err := stdio.ProtectStdout()
	if err != nil {
		slog.Error("worker: protect stdout", "err", err)
		return 1
	}
	info, err := userinfo.Current()
	if err != nil {
		slog.Error("worker: look up current user", "err", err)
		return 1
	}
	w := New(info, Options{})
	hello := protocol.WorkerHello{
		Protocol: protocol.Version,
		Version:  buildinfo.Version,
		User:     info.Name,
		UID:      int(info.UID),
		Home:     info.Home,
	}
	if err := protocol.WriteLine(proto, hello); err != nil {
		slog.Error("worker: write hello", "err", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer stop()

	conn := &stdio.Conn{Reader: os.Stdin, Writer: proto, Closers: []io.Closer{os.Stdin, proto}}
	slog.Info("worker: started", "user", info.Name, "uid", info.UID)
	if err := Serve(ctx, conn, w.Handler()); err != nil {
		slog.Error("worker: serve", "err", err)
	}
	w.Shutdown()
	slog.Info("worker: stopped", "user", info.Name)
	return 0
}
