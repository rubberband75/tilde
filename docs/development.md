# Development and testing

## Setup on Linux Mint

```bash
sudo apt install build-essential libpam0g-dev git
# Go 1.24+: https://go.dev/dl/ (or an older apt Go; go.mod fetches the toolchain)
# Node 22+: e.g. nvm install 22
make deps
```

## Day to day

Two terminals:

```bash
make dev-server   # Go server on http://127.0.0.1:8443 (--dev, single-user)
make dev-web      # Vite on http://localhost:5173 with hot reload
```

Log in at http://localhost:5173 with your own account. Vite proxies `/api`
(including WebSockets) to the Go server; `changeOrigin` stays off so the
Origin check passes.

`make dev-server` rebuilds the Go binary each time. For other accounts and
real logind sessions use `make dev-server-root` (sudo).

Production-style run with the UI embedded:

```bash
make build
sudo ./bin/tilde serve --listen 127.0.0.1:8443   # https, self-signed
```

## Tests

```bash
make test        # go vet, go test -race ./..., vue-tsc
```

- `internal/worker/worker_test.go` runs the worker's real HTTP API over yamux
  on an in-memory pipe: file lifecycle, Range downloads, trash spec format,
  and a real `/bin/sh` on a PTY with reattach/replay. Add a test here for every
  new endpoint.
- `internal/server/server_test.go`: tokens, rate limiting, Origin checks,
  auth required, SPA fallback.
- `internal/protocol`: the pipe handshake never consumes bytes meant for yamux.

`make server-nopam` and `go test -tags nopam ./...` work without libpam.

## End-to-end tests (real PAM, real privilege drop)

`server/e2e` logs in through the running server and checks that the worker
and its shells run as the user, that the kernel refuses `/root`, that
cross-origin requests are rejected and that logout ends the session.

Run them **only in a disposable VM** (they create sessions and run commands
as a real account):

```bash
# in a Mint 22 VM
sudo apt install build-essential libpam0g-dev
sudo useradd -m -s /bin/bash tester && echo 'tester:tester-pass' | sudo chpasswd
make build
sudo install -m 0644 packaging/pam.d/tilde /etc/pam.d/tilde
sudo install -m 0755 bin/tilde /usr/local/bin/tilde
sudo /usr/local/bin/tilde serve --dev --listen 127.0.0.1:8443 &
make e2e TILDE_URL=http://127.0.0.1:8443 TILDE_USER=tester TILDE_PASS=tester-pass
```

Run the binary from a path the test user can read (e.g. `/usr/local/bin`);
the helper re-executes it as the user to start the worker.

Things to check by hand in the VM, since they depend on a real systemd host:

- `loginctl list-sessions` shows a session of type `web` for the user.
- `echo $XDG_RUNTIME_DIR` and `busctl --user` work in a Tilde terminal.
- Logging out removes the session; `nohup sleep 1000 &` survives it.
- `sudo` works in the terminal and asks for the password.

## Debugging

- `tilde serve --debug` (or `TILDE_DEBUG=1`) for verbose logs. Helper and
  worker logs appear in the server log as `session log` lines with the user.
- PAM problems: `journalctl -t tilde` and `journalctl SYSLOG_FACILITY=10`.
- A blank or broken stream usually means something wrote to the worker's
  stdout. Search for `fmt.Print` and C libraries that print.

## Icons

`make icons` refreshes `web/public/icons/Mint-Y` from
`/usr/share/icons/Mint-Y` (installed on Mint) or a sparse clone of
linuxmint/mint-y-icons. Add icon names to `web/icons.txt` first.
