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
make test        # gofmt + Prettier checks, go vet, go test -race ./..., vue-tsc, Vitest
make fmt         # fix formatting (gofmt -w, prettier --write)
```

- `internal/worker/worker_test.go` runs the worker's real HTTP API over yamux
  on an in-memory pipe: file lifecycle, Range downloads, trash spec format,
  and a real `/bin/sh` on a PTY with reattach/replay. Add a test here for every
  new endpoint.
- `internal/server/server_test.go`: tokens, rate limiting, Origin checks,
  auth required, SPA fallback.
- `internal/protocol`: the pipe handshake never consumes bytes meant for yamux.
- `web/src/**/*.test.ts` (Vitest, jsdom): the window store (open, focus,
  minimize, close guards, layout persistence), path helpers and session-expiry
  handling in the API client. Put tests next to the code they cover.
- `web/e2e/*.spec.ts` (Playwright): the UI in a real browser against a real
  install. They need a throwaway account, so they run in the e2e VM (below).

`make server-nopam` and `go test -tags nopam ./...` work without libpam.

CI (`.github/workflows/ci.yml`) runs `make test` and the builds on every push
and pull request.

## End-to-end tests (real PAM, real privilege drop)

`server/e2e` logs in through the running server and checks that the worker
and its shells run as the user, that the kernel refuses `/root`, that the
shell is in a logind session of type `web` with `XDG_RUNTIME_DIR` and a
session D-Bus, that cross-origin requests are rejected, that logout
closes the logind session while `nohup` jobs survive, that file jobs get the
kernel's answer for the user (no reading `/etc/shadow`), and that 25 logins
in a row all open a PAM session (D32).

They create sessions and run commands as a real account, so they run **only
in a disposable VM** (D29):

```bash
sudo apt install qemu-system-x86 qemu-utils genisoimage   # once
make e2e-vm
```

`scripts/e2e-vm.sh` boots the Ubuntu 24.04 cloud image (Mint 22's base) under
QEMU/KVM with cloud-init, creates a `tester` account (password
`tester-pass`, not in sudo), builds Tilde on the host, installs it in the VM
the way `make install` does (real PAM file, real systemd unit, HTTPS with a
self-signed certificate) and runs the suite there. The VM keeps running
between runs, so the next `make e2e-vm` takes seconds.

```bash
scripts/e2e-vm.sh ssh        # shell in the VM (user ubuntu, passwordless sudo)
scripts/e2e-vm.sh logs       # journalctl -u tilde
scripts/e2e-vm.sh down       # power off
scripts/e2e-vm.sh reset      # delete the disk; the next run starts from scratch
```

State lives in `~/.cache/tilde-e2e-vm` (`TILDE_VM_DIR`); SSH is forwarded to
`127.0.0.1:2522` (`TILDE_VM_SSH_PORT`). To use the UI against the VM:
`ssh -i ~/.cache/tilde-e2e-vm/id_ed25519 -p 2522 -L 9443:127.0.0.1:8443
ubuntu@127.0.0.1`, then open https://localhost:9443 and log in as `tester`
(or run `scripts/e2e-vm.sh tunnel`). The VM build embeds the UI.

After the Go suite, `make e2e-vm` runs the Playwright UI tests in
`web/e2e` with Chromium on the host, through an SSH tunnel to the VM
(`127.0.0.1:9444`, `TILDE_VM_UI_TEST_PORT`), logged in as `tester`: login
and logout, the desktop icons, layout and background, Files (new folder,
rename, trash and restore), live updates from inotify, the terminal, and
per-drive trash on a small ext4 image the script loop-mounts at
`/mnt/tilde-drive` for `tester` (`TILDE_DRIVE`), and ejecting a "USB
stick": a FAT image labelled `TILDE-USB` that `tester` sets up and mounts
through udisks before each run (`TILDE_USB`; the Go suite's
`TestDrivesFollowUdisks` uses it too). A test-only polkit rule in the VM
(`/etc/polkit-1/rules.d/49-tilde-e2e.rules`) lets `tester` do that without
an active session. Tests whose variable is unset skip. `make deps` installs Playwright; the first run downloads its
Chromium (on a host without the browser's libraries: `cd web && npx
playwright install --with-deps chromium`). Output is kept in
`~/.cache/tilde-e2e-vm/last-ui-test.log`; on failure, `cd web && npx
playwright show-report` has screenshots and traces.

```bash
scripts/e2e-vm.sh ui                 # only the UI suite, against what is installed
scripts/e2e-vm.sh ui -- -g Files     # arguments after -- go to "playwright test"
make e2e-ui TILDE_URL=https://localhost:9443 TILDE_USER=tester TILDE_PASS=tester-pass
```

UI tests share the one test user, so they run one at a time and remove what
they create (fixtures in `web/e2e/fixtures.ts`). Write files for a test
through the `api` fixture rather than the UI when the UI is not what is
being tested; changes made that way reach the page only through the
worker's inotify events, which is also how the live-update tests work.

Still checked by hand: `sudo` works in a Tilde terminal and asks for the
password (give `tester` sudo first: `scripts/e2e-vm.sh ssh -- sudo usermod
-aG sudo tester`, then log in through the UI as described above).

The VM has no desktop, so Cinnamon-specific behavior (gsettings, the GNOME
keyring) still needs a real Mint install to check by hand.

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
