// Package buildinfo holds values stamped into the binary at build time.
package buildinfo

// Version is set with -ldflags "-X tilde/internal/buildinfo.Version=v0.1.0".
var Version = "dev"
