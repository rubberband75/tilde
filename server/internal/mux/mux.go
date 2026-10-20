// Package mux holds the yamux settings shared by the server and the worker.
package mux

import (
	"os"
	"time"

	"github.com/hashicorp/yamux"
)

// Config returns the yamux configuration used on both ends of the pipe.
func Config() *yamux.Config {
	c := yamux.DefaultConfig()
	// The default 256 KiB window throttles large file transfers; 16 MiB keeps
	// a fast download from waiting on round trips. Fairness between streams
	// (a download vs. terminal keystrokes) comes from per-stream windows.
	c.MaxStreamWindowSize = 16 * 1024 * 1024
	c.EnableKeepAlive = true
	c.KeepAliveInterval = 20 * time.Second
	c.ConnectionWriteTimeout = 30 * time.Second
	c.LogOutput = os.Stderr
	return c
}
