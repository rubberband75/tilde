// Package helper is the per-login session helper ("tilde session-helper").
//
// The main server starts one helper per login attempt, as root, with a pipe
// on stdin/stdout. The helper:
//
//  1. reads a HelperRequest (user, password, mode),
//  2. authenticates through PAM,
//  3. in session mode, opens a PAM session (so pam_systemd registers a
//     logind session of type "web"), then starts "tilde worker" as the user
//     with the same stdin/stdout pipe,
//  4. waits for the worker to exit and closes the PAM session.
//
// The helper must stay alive for the whole session: pam_close_session has to
// run in the process that opened it.
//
// When the server is not running as root (development), the helper runs in
// single-user mode: only the account that started the server can log in, no
// PAM session is opened, and the worker runs without changing IDs.
package helper

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sort"
	"syscall"

	"tilde/internal/pamauth"
	"tilde/internal/protocol"
	"tilde/internal/stdio"
	"tilde/internal/userinfo"
)

// Run executes the helper and returns the process exit code.
func Run() int {
	proto, err := stdio.ProtectStdout()
	if err != nil {
		slog.Error("helper: protect stdout", "err", err)
		return 1
	}
	defer proto.Close()

	var req protocol.HelperRequest
	if err := protocol.ReadLineUnbuffered(os.Stdin, &req); err != nil {
		reply(proto, failure(protocol.CodeInternal, "read request: "+err.Error()))
		return 1
	}
	password := req.Password
	req.Password = ""

	privileged := os.Geteuid() == 0

	if code, msg := precheck(req.User, privileged); code != "" {
		reply(proto, failure(code, msg))
		return 1
	}

	sess, err := pamauth.Authenticate(req.User, password, req.RemoteAddr)
	password = ""
	if err != nil {
		slog.Warn("helper: authentication failed", "user", req.User, "rhost", req.RemoteAddr, "err", err)
		reply(proto, failureFor(err))
		return 1
	}
	defer sess.Close()

	info, err := userinfo.Lookup(req.User)
	if err != nil {
		reply(proto, failure(protocol.CodeInternal, "look up account: "+err.Error()))
		return 1
	}
	if info.UID == 0 {
		reply(proto, failure(protocol.CodeRootDisabled, rootMessage))
		return 1
	}

	if req.Mode == protocol.ModeVerify {
		reply(proto, protocol.HelperResult{OK: true, UID: int(info.UID)})
		return 0
	}
	if req.Mode != protocol.ModeSession {
		reply(proto, failure(protocol.CodeInternal, "unknown mode "+req.Mode))
		return 1
	}

	env := baseEnv(info)
	if privileged {
		pamEnv, err := sess.Open(map[string]string{
			"XDG_SESSION_TYPE":    "web",
			"XDG_SESSION_CLASS":   "user",
			"XDG_SESSION_DESKTOP": "tilde",
		})
		if err != nil {
			slog.Error("helper: open session", "user", req.User, "err", err)
			reply(proto, failure(protocol.CodeInternal, "could not open a login session"))
			return 1
		}
		for k, v := range pamEnv {
			env[k] = v
		}
	} else {
		slog.Warn("helper: single-user development mode, no PAM session opened", "user", req.User)
		if v := os.Getenv("XDG_RUNTIME_DIR"); v != "" {
			env["XDG_RUNTIME_DIR"] = v
		}
	}

	self, err := os.Executable()
	if err != nil {
		reply(proto, failure(protocol.CodeInternal, "find executable: "+err.Error()))
		return 1
	}

	reply(proto, protocol.HelperResult{OK: true, UID: int(info.UID)})
	return runWorker(self, info, env, proto, privileged)
}

const rootMessage = "Logging in as root is disabled. Log in as a regular user and use sudo."

func precheck(user string, privileged bool) (code, msg string) {
	if user == "" {
		return protocol.CodeAuthFailed, "authentication failed"
	}
	if user == "root" {
		return protocol.CodeRootDisabled, rootMessage
	}
	if !privileged {
		me, err := userinfo.Current()
		if err != nil || me.Name != user {
			return protocol.CodeNotAllowed, "Tilde is running without root (development mode); only the account that started it can log in."
		}
	}
	return "", ""
}

// runWorker starts the worker on the helper's own stdin and protocol stdout
// and waits for it.
func runWorker(self string, info *userinfo.Info, env map[string]string, proto *os.File, privileged bool) int {
	// Pdeathsig is tied to the OS thread that forks the child, so keep this
	// goroutine on one thread until the worker exits.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	cmd := exec.Command(self, "worker")
	cmd.Stdin = os.Stdin
	cmd.Stdout = proto
	cmd.Stderr = os.Stderr
	cmd.Env = envList(env)
	cmd.Dir = info.Home
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Pdeathsig: syscall.SIGTERM}
	if privileged {
		cmd.SysProcAttr.Credential = &syscall.Credential{Uid: info.UID, Gid: info.GID, Groups: info.Groups}
	}
	if err := cmd.Start(); err != nil {
		slog.Error("helper: start worker", "user", info.Name, "err", err)
		return 1
	}
	// The worker holds its own copy of the pipe. Dropping ours means the
	// server sees EOF as soon as the worker exits.
	proto.Close()
	os.Stdin.Close()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		for sig := range sigs {
			_ = cmd.Process.Signal(sig)
		}
	}()

	err := cmd.Wait()
	signal.Stop(sigs)
	close(sigs)
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		slog.Info("helper: worker exited", "user", info.Name)
		return 0
	case errors.As(err, &exitErr):
		slog.Info("helper: worker exited", "user", info.Name, "status", exitErr.ExitCode())
		return 0
	default:
		slog.Error("helper: wait for worker", "user", info.Name, "err", err)
		return 1
	}
}

func baseEnv(info *userinfo.Info) map[string]string {
	return map[string]string{
		"HOME":    info.Home,
		"USER":    info.Name,
		"LOGNAME": info.Name,
		"SHELL":   info.Shell,
		"PATH":    "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/usr/games:/usr/local/games",
		"LANG":    "C.UTF-8",
	}
}

func envList(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, fmt.Sprintf("%s=%s", k, v))
	}
	sort.Strings(out)
	return out
}

func failure(code, msg string) protocol.HelperResult {
	return protocol.HelperResult{OK: false, Code: code, Error: msg}
}

func failureFor(err error) protocol.HelperResult {
	switch {
	case errors.Is(err, pamauth.ErrPasswordExpired):
		return failure(protocol.CodePasswordExpired, err.Error())
	case errors.Is(err, pamauth.ErrAccountExpired):
		return failure(protocol.CodeAccountExpired, err.Error())
	case errors.Is(err, pamauth.ErrUnavailable):
		return failure(protocol.CodeInternal, err.Error())
	}
	// Do not reveal whether the user exists.
	return failure(protocol.CodeAuthFailed, "authentication failed")
}

func reply(w *os.File, res protocol.HelperResult) {
	if err := protocol.WriteLine(w, res); err != nil {
		slog.Error("helper: write result", "err", err)
	}
}
