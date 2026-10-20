// Command tilde is a browser-based Linux desktop.
//
// One binary, three roles:
//
//	tilde serve           main server (root): TLS, login, proxy to workers
//	tilde session-helper  per-login PAM helper (root), started by serve
//	tilde worker          per-user API process, started by the helper
//
// Only "serve" is meant to be run by hand; the other two speak a pipe
// protocol (see internal/protocol).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"tilde/internal/buildinfo"
	"tilde/internal/helper"
	"tilde/internal/server"
	"tilde/internal/webui"
	"tilde/internal/worker"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "serve":
		os.Exit(serve(args))
	case "session-helper":
		setupLogging("helper", false)
		os.Exit(helper.Run())
	case "worker":
		setupLogging("worker", false)
		os.Exit(worker.Run())
	case "version", "--version", "-v":
		fmt.Println("tilde", buildinfo.Version)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "tilde: unknown command %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `Tilde %s, a browser-based Linux desktop.

Usage:
  tilde serve [flags]   start the server (run as root, usually via systemd)
  tilde version         print the version

Run "tilde serve -h" for server flags.
`, buildinfo.Version)
}

func serve(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	listen := fs.String("listen", "127.0.0.1:8443", "address to listen on (host:port)")
	dev := fs.Bool("dev", false, "development mode: plain HTTP on loopback, for use behind the Vite dev server")
	cert := fs.String("tls-cert", "", "TLS certificate (PEM); default: self-signed under the state directory")
	key := fs.String("tls-key", "", "TLS private key (PEM)")
	stateDir := fs.String("state-dir", "/var/lib/tilde", "directory for server state (TLS certificate)")
	origins := fs.String("allowed-origins", "", "comma-separated extra origins to accept, e.g. https://tilde.example.com")
	trustProxy := fs.Bool("trust-proxy", false, "trust X-Forwarded-For (only behind your own reverse proxy)")
	debug := fs.Bool("debug", false, "verbose logging")
	fs.Parse(args)

	setupLogging("server", *debug)

	var extra []string
	for _, o := range strings.Split(*origins, ",") {
		if o = strings.TrimSpace(o); o != "" {
			extra = append(extra, o)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	err := server.Run(ctx, server.Config{
		Listen:         *listen,
		Dev:            *dev,
		TLSCert:        *cert,
		TLSKey:         *key,
		StateDir:       *stateDir,
		AllowedOrigins: extra,
		TrustProxy:     *trustProxy,
		Web:            webui.FS(),
	})
	if err != nil {
		slog.Error("tilde serve", "err", err)
		return 1
	}
	return 0
}

// setupLogging sends structured logs to stderr. The helper's and worker's
// stderr is collected by the server and tagged with the user.
func setupLogging(component string, debug bool) {
	level := slog.LevelInfo
	if debug || os.Getenv("TILDE_DEBUG") == "1" {
		level = slog.LevelDebug
	}
	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(h).With("component", component))
}
