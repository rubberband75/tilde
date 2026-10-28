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
	// drafts holds the text editor's unsaved changes (D48).
	drafts    string
	cacheHome string
	umask     os.FileMode
	trash     *Trash
	terms     *terminalManager
	events    *eventHub
	watcher   *dirWatcher
	drives    *driveService
	network   *networkService
	procs     *processSampler
	services  *serviceManagers
	resources *resourceMonitor
	jobs      *jobManager
	logind    *logindConn
	polkit    *polkitAgent
	auditQ    *auditQueue
}

// Options overrides locations, mainly for tests.
type Options struct {
	DataHome   string // $XDG_DATA_HOME, default ~/.local/share
	ConfigHome string // $XDG_CONFIG_HOME, default ~/.config
	CacheHome  string // $XDG_CACHE_HOME, default ~/.cache
	StateHome  string // $XDG_STATE_HOME, default ~/.local/state
	// ThumbnailerDirs overrides where *.thumbnailer files are looked up.
	ThumbnailerDirs []string
	// LogindSession is the id of the worker's logind session (D41), which
	// Run connects to after the hello. Without it, Session (tests) or
	// nothing is followed, and logind is left alone.
	LogindSession string
	Session       logindSession
}

// New builds a Worker for info.
func New(info *userinfo.Info, opt Options) *Worker {
	if opt.DataHome == "" {
		opt.DataHome = xdgDir("XDG_DATA_HOME", filepath.Join(info.Home, ".local", "share"))
	}
	if opt.ConfigHome == "" {
		opt.ConfigHome = xdgDir("XDG_CONFIG_HOME", filepath.Join(info.Home, ".config"))
	}
	if opt.StateHome == "" {
		opt.StateHome = xdgDir("XDG_STATE_HOME", filepath.Join(info.Home, ".local", "state"))
	}
	if opt.CacheHome == "" {
		opt.CacheHome = xdgDir("XDG_CACHE_HOME", filepath.Join(info.Home, ".cache"))
	}
	if opt.ThumbnailerDirs == nil {
		opt.ThumbnailerDirs = thumbnailerDirs(opt.DataHome)
	}
	setThumbnailers(loadThumbnailers(opt.ThumbnailerDirs))
	host, _ := os.Hostname()
	trash := NewTrash(opt.DataHome)
	events := newEventHub()
	w := &Worker{
		info:       info,
		hostname:   host,
		configHome: opt.ConfigHome,
		drafts:     filepath.Join(opt.StateHome, "tilde", "drafts"),
		cacheHome:  opt.CacheHome,
		umask:      readUmask(),
		trash:      trash,
		terms:      newTerminalManager(info.Shell, info.Home),
		events:     events,
		watcher:    newDirWatcher(func(dirs []string) { events.publish(event{Type: "fs", Dirs: dirs}) }),
		drives:     newDriveService(func() { events.publish(event{Type: "drives"}) }),
		network:    newNetworkService(func() { events.publish(event{Type: "network"}) }),
		procs:      &processSampler{},
		services:   &serviceManagers{},
		resources:  newResourceMonitor(),
		jobs:       newJobManager(events, trash),
		logind:     newLogindConn(opt),
		polkit:     newPolkitAgent(info.Name, func() { events.publish(event{Type: "polkit"}) }),
		auditQ:     newAuditQueue(),
	}
	w.terms.exited = func(id string) { w.audit(nil, protocol.AuditEvent{Event: "terminal_close", Terminal: id}) }
	w.polkit.audit = w.auditQ.add
	return w
}

func newLogindConn(opt Options) *logindConn {
	if opt.LogindSession != "" {
		return &logindConn{id: opt.LogindSession, ready: make(chan struct{})}
	}
	return readyLogind(opt.Session)
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
	m.HandleFunc("GET /api/v1/drives", w.handleDrives)
	m.HandleFunc("POST /api/v1/drives/mount", w.handleMount)
	m.HandleFunc("POST /api/v1/drives/unmount", w.handleUnmount)
	m.HandleFunc("POST /api/v1/drives/eject", w.handleEject)
	m.HandleFunc("GET /api/v1/processes", w.handleProcesses)
	m.HandleFunc("POST /api/v1/processes/signal", w.handleProcessSignal)
	m.HandleFunc("GET /api/v1/services", w.handleServices)
	m.HandleFunc("POST /api/v1/services/action", w.handleServiceAction)
	m.HandleFunc("GET /api/v1/network", w.handleNetwork)
	m.HandleFunc("POST /api/v1/network/wifi", w.handleWifiSwitch)
	m.HandleFunc("POST /api/v1/network/scan", w.handleNetworkScan)
	m.HandleFunc("POST /api/v1/network/connect", w.handleNetworkConnect)
	m.HandleFunc("POST /api/v1/network/disconnect", w.handleNetworkDisconnect)
	m.HandleFunc("GET /api/v1/bookmarks", w.handleBookmarks)
	m.HandleFunc("POST /api/v1/bookmarks", w.handleBookmarkAdd)
	m.HandleFunc("DELETE /api/v1/bookmarks", w.handleBookmarkRemove)
	m.HandleFunc("GET /api/v1/desktop/layout", w.handleDesktopLayout)
	m.HandleFunc("POST /api/v1/desktop/layout", w.handleDesktopLayoutSave)
	m.HandleFunc("GET /api/v1/desktop/background", w.handleBackground)
	m.HandleFunc("PUT /api/v1/desktop/background", w.handleBackgroundSet)
	m.HandleFunc("GET /api/v1/desktop/backgrounds", w.handleBackgrounds)

	m.HandleFunc("GET /api/v1/fs/list", w.handleList)
	m.HandleFunc("GET /api/v1/fs/stat", w.handleStat)
	m.HandleFunc("GET /api/v1/fs/search", w.handleSearch)
	m.HandleFunc("GET /api/v1/fs/properties", w.handleProperties)
	m.HandleFunc("GET /api/v1/fs/usage", w.handleUsage)
	m.HandleFunc("POST /api/v1/fs/chmod", w.handleChmod)
	m.HandleFunc("GET /api/v1/fs/content", w.handleDownload)
	m.HandleFunc("GET /api/v1/fs/thumbnail", w.handleThumbnail)
	m.HandleFunc("PUT /api/v1/fs/content", w.handleUpload)
	m.HandleFunc("POST /api/v1/fs/mkdir", w.handleMkdir)
	m.HandleFunc("POST /api/v1/fs/rename", w.handleRename)
	m.HandleFunc("POST /api/v1/fs/trash", w.handleTrashPut)

	m.HandleFunc("GET /api/v1/text", w.handleTextGet)
	m.HandleFunc("PUT /api/v1/text", w.handleTextPut)
	m.HandleFunc("GET /api/v1/drafts", w.handleDrafts)
	m.HandleFunc("PUT /api/v1/drafts", w.handleDraftPut)
	m.HandleFunc("DELETE /api/v1/drafts", w.handleDraftDelete)

	m.HandleFunc("GET /api/v1/trash", w.handleTrashList)
	m.HandleFunc("POST /api/v1/trash/restore", w.handleTrashRestore)
	m.HandleFunc("POST /api/v1/trash/empty", w.handleTrashEmpty)

	m.HandleFunc("GET /api/v1/jobs", w.handleJobList)
	m.HandleFunc("POST /api/v1/jobs", w.handleJobStart)
	m.HandleFunc("GET /api/v1/jobs/{id}", w.handleJobGet)
	m.HandleFunc("DELETE /api/v1/jobs/{id}", w.handleJobDelete)
	m.HandleFunc("POST /api/v1/jobs/{id}/cancel", w.handleJobCancel)
	m.HandleFunc("POST /api/v1/jobs/{id}/resolve", w.handleJobResolve)

	m.HandleFunc("GET /api/v1/events", w.handleEvents)

	m.HandleFunc("GET /api/v1/polkit", w.handlePolkit)
	m.HandleFunc("POST /api/v1/polkit/respond", w.handlePolkitRespond)
	m.HandleFunc("POST /api/v1/polkit/cancel", w.handlePolkitCancel)

	m.HandleFunc("GET /api/v1/terminals", w.handleTerminalList)
	m.HandleFunc("POST /api/v1/terminals", w.handleTerminalCreate)
	m.HandleFunc("DELETE /api/v1/terminals/{id}", w.handleTerminalDelete)
	m.HandleFunc("GET /api/v1/terminals/{id}/attach", w.handleTerminalAttach)

	// For the server only (protocol.InternalHeader), never proxied.
	m.HandleFunc("GET /internal/session", w.handleSession)
	m.HandleFunc("PUT /internal/session/locked", w.handleSessionLocked)

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

// Shutdown stops file jobs and hangs up all terminals, as logging out does.
func (w *Worker) Shutdown() {
	w.watcher.close()
	w.drives.close()
	w.network.close()
	w.services.close()
	w.resources.close()
	w.polkit.close()
	w.jobs.cancelAll()
	w.terms.closeAll()
	select {
	case <-w.logind.ready:
		if w.logind.s != nil {
			w.logind.s.close()
		}
	default: // still connecting; the process is about to exit
	}
}

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
	w := New(info, Options{LogindSession: os.Getenv("XDG_SESSION_ID")})
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
	if w.logind.id != "" {
		go func() {
			w.logind.connect()
			// Only for Tilde's own web session: started from inside
			// another session, the worker shares that session's agent.
			if w.logind.s != nil {
				w.polkit.start(w.logind.id)
			}
		}()
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
