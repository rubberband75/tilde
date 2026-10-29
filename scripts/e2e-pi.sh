#!/usr/bin/env bash
# Runs a small part of Tilde's end-to-end tests on a Raspberry Pi (D64,
# D74): real Raspberry Pi OS, kernel and hardware, which QEMU cannot give.
# Only on request (make e2e-pi), so the usual run stays as fast as it is.
#
# The Pi must be one you can wipe: the run installs Tilde from its arm64
# .deb, adds a test account and leaves both there. To say a Pi is such a
# one, run on it once:   sudo touch /etc/tilde-e2e-disposable
# Without that file the script stops before changing anything.
#
# Usage: scripts/e2e-pi.sh user@host [command]
#   test     build on the Pi, package on this machine, install the .deb on
#            the Pi, run the Go and UI subsets (default)
#   ui       rerun only the UI subset against what is installed (args go to
#            "playwright test")
#   logs     show the tilde service log on the Pi
#   tunnel   forward https://localhost:9450 to Tilde on the Pi (Ctrl+C stops)
#
# Needs: ssh to the Pi with a key, a user there with passwordless sudo
# (Raspberry Pi OS's first user has it), internet on the Pi the first time
# (Go and build packages), 64-bit Raspberry Pi OS. Go (the version this
# machine has) is kept in ~/tilde-e2e on the Pi, not installed system-wide.
# Environment: TILDE_PI_UI_PORT (default 9450), TILDE_PI_UI_TEST_PORT
# (default 9453, the UI subset's own tunnel). Each tunnel also forwards the
# two ports after it to Tilde's app port (D76) and Xpra port (D78).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${TILDE_PI_DIR:-$HOME/.cache/tilde-e2e-pi}"
UI_PORT="${TILDE_PI_UI_PORT:-9450}"
UI_TEST_PORT="${TILDE_PI_UI_TEST_PORT:-9453}"
NFPM=github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.43.1

# The throwaway account the tests log in as. Not in sudo.
TEST_USER=tester
TEST_PASS=tester-pass

# Go tests that need no more than an account: logins and sessions, the
# privilege drop, locking, jobs, the audit log.
GO_TESTS='^(TestWorkerRunsAsTheUser|TestLogindSession|TestCrossOriginRefused|TestLogoutEndsSession|TestLogoutClosesLogindSession|TestLockAndUnlock|TestJobsRunAsTheUser|TestAuditLog|TestRecentLoginsIgnoreForgeries)$'
# UI specs likewise: the desktop, the apps most used, settings.
UI_SPECS=(login desktop windows terminal files editor settings)

log() { printf '\033[1m==> %s\033[0m\n' "$*" >&2; }
die() { printf 'e2e-pi: %s\n' "$*" >&2; exit 1; }

[ $# -ge 1 ] && [[ "$1" == *@* ]] || die "usage: $0 user@host [test|ui|logs|tunnel] (or: make e2e-pi PI=user@host)"
PI="$1"
shift

pi_ssh() {
  ssh -q -o ConnectTimeout=10 -o BatchMode=yes -o ServerAliveInterval=15 "$PI" "$@"
}

# pi_tunnel runs in the background: exec, so that its pid ($!) is ssh's,
# and killing it ends the tunnel rather than leaving ssh behind.
pi_tunnel() {
  exec ssh -q -o ConnectTimeout=10 -o BatchMode=yes -o ServerAliveInterval=15 -N -o ExitOnForwardFailure=yes "$PI" "$@"
}

pi_scp() {
  scp -q -o BatchMode=yes "$@"
}

# Checks it is a 64-bit Pi that may be wiped, before anything changes.
check_pi() {
  pi_ssh true || die "cannot ssh to $PI without a password (ssh-copy-id $PI)"
  pi_ssh test -e /etc/tilde-e2e-disposable ||
    die "$PI is not marked as disposable; if nothing on it matters, run there: sudo touch /etc/tilde-e2e-disposable"
  [ "$(pi_ssh uname -m)" = aarch64 ] || die "$PI is not 64-bit ARM (64-bit Raspberry Pi OS is needed, D65)"
  pi_ssh sudo -n true 2>/dev/null || die "$(echo "$PI" | cut -d@ -f1) needs passwordless sudo on the Pi"
  local model os
  model="$(pi_ssh "tr -d '\\0' </proc/device-tree/model 2>/dev/null || echo 'not a Raspberry Pi'")"
  os="$(pi_ssh '. /etc/os-release && echo "$PRETTY_NAME"')"
  log "Testing on $model, $os"
}

# Builds on the Pi (cgo, for PAM, is native there), packages the binary
# here with the same nfpm as "make deb", and installs the package with
# apt, so its dependencies and scripts are tested on Raspberry Pi OS.
install_build() {
  local gover
  gover="$(cd "$ROOT/server" && go env GOVERSION)"
  log "Preparing the Pi: build packages, $gover, the test account"
  pi_ssh "set -e
    if ! dpkg -s build-essential libpam0g-dev curl >/dev/null 2>&1; then
      sudo apt-get update -q >/dev/null
      sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -q build-essential libpam0g-dev curl >/dev/null
    fi
    mkdir -p ~/tilde-e2e
    if [ ! -x ~/tilde-e2e/$gover/bin/go ]; then
      rm -rf ~/tilde-e2e/go*
      mkdir -p ~/tilde-e2e/$gover
      curl -fsSL https://go.dev/dl/$gover.linux-arm64.tar.gz | tar -C ~/tilde-e2e/$gover --strip-components=1 -xz
    fi
    id $TEST_USER >/dev/null 2>&1 || sudo useradd -m -s /bin/bash $TEST_USER
    echo '$TEST_USER:$TEST_PASS' | sudo chpasswd"

  log "Building the UI here and Tilde on the Pi"
  need_here go npm
  make -s -C "$ROOT" web >/dev/null
  mkdir -p "$OUT"
  local version
  version="$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo dev)"
  # The source with the UI embedded; nothing else of the tree is needed.
  tar -C "$ROOT" -cz --exclude='*.test' server | pi_ssh "rm -rf ~/tilde-e2e/src && mkdir -p ~/tilde-e2e/src && tar -C ~/tilde-e2e/src -xz"
  pi_ssh "set -e
    cd ~/tilde-e2e/src/server
    PATH=~/tilde-e2e/$gover/bin:\$PATH CGO_ENABLED=1 GOTOOLCHAIN=local \
      go build -trimpath -ldflags '-s -w -X tilde/internal/buildinfo.Version=$version' -o ~/tilde-e2e/tilde ./cmd/tilde"
  pi_scp "$PI:tilde-e2e/tilde" "$OUT/tilde"
  # The tests are plain Go: built here for the Pi.
  (cd "$ROOT/server" && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c -tags e2e -o "$OUT/e2e.test" ./e2e)

  rm -f "$OUT"/tilde_*.deb
  (cd "$ROOT" && TILDE_VERSION="0.0.0+e2e.$(date +%s)" TILDE_ARCH=arm64 TILDE_BIN="$OUT/tilde" \
    go run "$NFPM" package -f packaging/nfpm.yaml -p deb -t "$OUT/" >/dev/null)

  log "Installing the package on the Pi"
  pi_scp "$OUT/e2e.test" "$OUT"/tilde_*.deb "$PI:tilde-e2e/"
  pi_ssh 'set -e
    cd ~/tilde-e2e
    sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -q --allow-downgrades \
      -o Dpkg::Options::=--force-confnew ./tilde_*.deb >/dev/null
    rm -f tilde_*.deb
    systemctl is-enabled --quiet tilde || { echo "the package did not enable tilde" >&2; exit 1; }'
  restart_tilde
}

need_here() {
  local missing=()
  for cmd in "$@"; do command -v "$cmd" >/dev/null || missing+=("$cmd"); done
  [ ${#missing[@]} -eq 0 ] || die "missing ${missing[*]} on this machine"
}

# A fresh server for each run: the login rate limit lives in memory, and
# the suites fail some logins on purpose.
restart_tilde() {
  pi_ssh 'set -e
    sudo systemctl restart tilde
    for i in $(seq 1 40); do
      curl -ksf -o /dev/null https://127.0.0.1:8443/ && exit 0
      sleep 0.5
    done
    echo "tilde did not start" >&2
    sudo journalctl -u tilde --no-pager -n 50 >&2
    exit 1'
}

run_tests() {
  log "Running the Go e2e subset on the Pi"
  local status=0
  pi_ssh "cd ~/tilde-e2e && TILDE_URL=https://127.0.0.1:8443 TILDE_USER=$TEST_USER TILDE_PASS=$TEST_PASS \
    ./e2e.test -test.v -test.count=1 -test.run '$GO_TESTS'" 2>&1 | tee "$OUT/last-test.log" || status=$?
  if [ "$status" -ne 0 ]; then
    log "Tests failed; tilde service log:"
    pi_ssh sudo journalctl -u tilde --no-pager -n 100 || true
  fi
  return "$status"
}

# The UI subset in Chromium here, against the Pi through an SSH tunnel.
run_ui_tests() {
  need_here npx
  [ -x "$ROOT/web/node_modules/.bin/playwright" ] || die "Playwright is not installed; run: make deps"
  (cd "$ROOT/web" && npx playwright install chromium) >/dev/null ||
    die "could not install Chromium; try: cd web && npx playwright install --with-deps chromium"
  restart_tilde

  # The app and Xpra ports too (D76, D78): browsers find them one and two
  # ports up.
  pi_tunnel -L "127.0.0.1:$UI_TEST_PORT:127.0.0.1:8443" -L "127.0.0.1:$((UI_TEST_PORT + 1)):127.0.0.1:8444" \
    -L "127.0.0.1:$((UI_TEST_PORT + 2)):127.0.0.1:8445" &
  local tunnel=$!
  # shellcheck disable=SC2064 # expand now: the pid is local
  trap "kill $tunnel 2>/dev/null" EXIT
  local i
  for i in $(seq 1 20); do
    curl -ksf -o /dev/null "https://127.0.0.1:$UI_TEST_PORT/" && break
    kill -0 "$tunnel" 2>/dev/null || die "the tunnel to the Pi failed (is port $UI_TEST_PORT free?)"
    sleep 0.5
  done

  log "Running the UI subset"
  local status=0 specs=()
  for s in "${UI_SPECS[@]}"; do specs+=("e2e/$s.spec.ts"); done
  [ $# -gt 0 ] && specs=("$@")
  (cd "$ROOT/web" && TILDE_URL="https://localhost:$UI_TEST_PORT" TILDE_USER=$TEST_USER TILDE_PASS=$TEST_PASS \
    npx playwright test "${specs[@]}") 2>&1 | tee "$OUT/last-ui-test.log" || status=$?
  kill "$tunnel" 2>/dev/null || true
  trap - EXIT
  if [ "$status" -ne 0 ]; then
    log "UI tests failed; traces and screenshots: cd web && npx playwright show-report"
  fi
  return "$status"
}

mkdir -p "$OUT"
cmd="${1:-test}"
[ $# -gt 0 ] && shift
case "$cmd" in
  test)
    check_pi
    install_build
    # Both subsets run even if the first fails; either failing fails the run.
    status=0
    run_tests || status=$?
    run_ui_tests || status=$?
    exit "$status"
    ;;
  ui)
    check_pi
    [ "${1:-}" = "--" ] && shift
    run_ui_tests "$@"
    ;;
  logs) pi_ssh sudo journalctl -u tilde --no-pager -n 200 ;;
  tunnel)
    log "Tilde on the Pi: https://localhost:$UI_PORT (user $TEST_USER, password $TEST_PASS)"
    pi_ssh -N -L "127.0.0.1:$UI_PORT:127.0.0.1:8443" -L "127.0.0.1:$((UI_PORT + 1)):127.0.0.1:8444" \
      -L "127.0.0.1:$((UI_PORT + 2)):127.0.0.1:8445"
    ;;
  *) die "unknown command '$cmd' (test, ui, logs, tunnel)" ;;
esac
