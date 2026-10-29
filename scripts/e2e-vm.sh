#!/usr/bin/env bash
# Runs Tilde's end-to-end tests in a disposable VM: real PAM, real root,
# real logind, real privilege drop. Never run them on a machine you care
# about (docs/development.md, D29).
#
# The VM is the Ubuntu 24.04 cloud image (Linux Mint 22's base: same PAM,
# systemd and glibc) under QEMU/KVM, set up by cloud-init. Tilde is built
# on the host, packaged as a .deb (D65) and installed in the VM with dpkg,
# with the real PAM file, systemd unit and install scripts, so the
# packaging is tested too.
#
# Usage: scripts/e2e-vm.sh [command]
#   test     start the VM if needed, install the current build, run the Go
#            e2e suite and then the Playwright UI suite (default)
#   ui       run only the UI suite against what is installed (args go to
#            "playwright test", e.g.: scripts/e2e-vm.sh ui -- -g Files)
#   up       start the VM (downloads the image the first time)
#   fetch    only download and verify the cloud image
#   ssh      open a shell in the VM (or run: scripts/e2e-vm.sh ssh -- cmd)
#   logs     show the tilde service log in the VM
#   tunnel   forward https://localhost:9443 to Tilde in the VM (Ctrl+C stops)
#   down     shut the VM down
#   reset    shut down and delete the VM disk; the next "up" starts fresh
#
# Needs: qemu-system-x86 qemu-utils genisoimage (apt), access to /dev/kvm.
# The UI suite runs Playwright's Chromium on the host through an SSH tunnel;
# "make deps" installs Playwright and the first run downloads Chromium.
# Environment: TILDE_VM_DIR (default ~/.cache/tilde-e2e-vm),
# TILDE_VM_SSH_PORT (default 2522), TILDE_VM_MEM (MiB, default 2048),
# TILDE_VM_UI_PORT (default 9443, for "tunnel"), TILDE_VM_UI_TEST_PORT
# (default 9445, the UI suite's own tunnel). Each tunnel also forwards the
# port after it to Tilde's app port (D76).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIR="${TILDE_VM_DIR:-$HOME/.cache/tilde-e2e-vm}"
PORT="${TILDE_VM_SSH_PORT:-2522}"
MEM="${TILDE_VM_MEM:-2048}"
UI_PORT="${TILDE_VM_UI_PORT:-9443}"
UI_TEST_PORT="${TILDE_VM_UI_TEST_PORT:-9445}"

IMAGE_URL="https://cloud-images.ubuntu.com/noble/current"
IMAGE_NAME="noble-server-cloudimg-amd64.img"
BASE="$DIR/$IMAGE_NAME"
DISK="$DIR/disk.qcow2"
SEED="$DIR/seed.iso"
KEY="$DIR/id_ed25519"
PIDFILE="$DIR/qemu.pid"

# The throwaway account the tests log in as. Not in sudo.
TEST_USER=tester
TEST_PASS=tester-pass
# A second filesystem the test user can write to, for per-drive trash: an
# ext4 image loop-mounted in the VM.
TEST_DRIVE=/mnt/tilde-drive
# A "USB stick" for the drives test: a FAT image the test user sets up and
# mounts through udisks before each UI run (the test ejects it).
TEST_USB=TILDE-USB
# A second account that also needs a TOTP code (pam_oath, D43), with the
# RFC 6238 test secret; the tests compute codes from it.
TEST_2FA_USER=tester2fa
TEST_2FA_SECRET=3132333435363738393031323334353637383930
# Wi-Fi networks for the network applet's test (D55), served in the VM by
# hostapd on mac80211_hwsim radios: one with WPA2, one open.
TEST_WIFI_SECURE=TildeSecure
TEST_WIFI_OPEN=TildeOpen
TEST_WIFI_PASS=tilde-wifi-pass
# Services for the System Monitor's Services tab (D60): a system one the
# test user may start and stop with their own password, and a user one.
TEST_SERVICE=tilde-e2e-service
TEST_USER_SERVICE=tilde-e2e-user

log() { printf '\033[1m==> %s\033[0m\n' "$*" >&2; }
die() { printf 'e2e-vm: %s\n' "$*" >&2; exit 1; }

need() {
  local missing=()
  for cmd in "$@"; do command -v "$cmd" >/dev/null || missing+=("$cmd"); done
  [ ${#missing[@]} -eq 0 ] && return
  die "missing ${missing[*]}; install with: sudo apt install qemu-system-x86 qemu-utils genisoimage"
}

vm_ssh() {
  ssh -q -i "$KEY" -p "$PORT" \
    -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR \
    -o ConnectTimeout=5 -o BatchMode=yes \
    ubuntu@127.0.0.1 "$@"
}

# vm_tunnel runs in the background: exec, so that its pid ($!) is ssh's,
# and killing it ends the tunnel rather than leaving ssh behind.
vm_tunnel() {
  exec ssh -q -i "$KEY" -p "$PORT" \
    -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR \
    -o ConnectTimeout=5 -o BatchMode=yes -N -o ExitOnForwardFailure=yes \
    ubuntu@127.0.0.1 "$@"
}

vm_scp() {
  scp -q -i "$KEY" -P "$PORT" \
    -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o BatchMode=yes \
    "$@"
}

running() { [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; }

fetch() {
  mkdir -p "$DIR"
  if [ -f "$BASE" ]; then return; fi
  log "Downloading $IMAGE_NAME"
  curl -fL --progress-bar -o "$BASE.part" "$IMAGE_URL/$IMAGE_NAME"
  curl -fsSL -o "$DIR/SHA256SUMS" "$IMAGE_URL/SHA256SUMS"
  local want got
  want="$(awk -v n="*$IMAGE_NAME" '$2 == n { print $1 }' "$DIR/SHA256SUMS")"
  got="$(sha256sum "$BASE.part" | cut -d' ' -f1)"
  [ -n "$want" ] && [ "$want" = "$got" ] || { rm -f "$BASE.part"; die "checksum mismatch for $IMAGE_NAME"; }
  mv "$BASE.part" "$BASE"
}

make_seed() {
  [ -f "$KEY" ] || ssh-keygen -q -t ed25519 -N '' -C tilde-e2e-vm -f "$KEY"
  local tmp
  tmp="$(mktemp -d)"
  cat >"$tmp/user-data" <<EOF
#cloud-config
hostname: tilde-e2e
users:
  - default
  - name: $TEST_USER
    shell: /bin/bash
    lock_passwd: false
ssh_authorized_keys:
  - $(cat "$KEY.pub")
chpasswd:
  expire: false
  users:
    - {name: $TEST_USER, password: $TEST_PASS, type: text}
ssh_pwauth: false
package_update: false
EOF
  printf 'instance-id: tilde-e2e\nlocal-hostname: tilde-e2e\n' >"$tmp/meta-data"
  genisoimage -quiet -output "$SEED" -volid cidata -joliet -rock "$tmp/user-data" "$tmp/meta-data"
  rm -rf "$tmp"
}

up() {
  need qemu-system-x86_64 qemu-img genisoimage
  [ -w /dev/kvm ] || die "no write access to /dev/kvm"
  if running; then return; fi
  fetch
  if [ ! -f "$DISK" ]; then
    log "Creating the VM disk"
    qemu-img create -q -f qcow2 -F qcow2 -b "$BASE" "$DISK" 10G
    make_seed
  fi
  log "Booting the VM (ssh on 127.0.0.1:$PORT)"
  qemu-system-x86_64 \
    -name tilde-e2e -machine q35,accel=kvm -cpu host -smp 2 -m "$MEM" \
    -drive "file=$DISK,if=virtio" \
    -drive "file=$SEED,if=virtio,format=raw,readonly=on" \
    -nic "user,model=virtio-net-pci,hostfwd=tcp:127.0.0.1:$PORT-:22" \
    -display none -serial "file:$DIR/serial.log" \
    -daemonize -pidfile "$PIDFILE"
  local i
  for i in $(seq 1 90); do
    vm_ssh true 2>/dev/null && break
    running || die "the VM exited during boot; see $DIR/serial.log"
    sleep 2
  done
  vm_ssh true 2>/dev/null || die "no ssh after 3 minutes; see $DIR/serial.log"
  log "Waiting for cloud-init"
  vm_ssh cloud-init status --wait >/dev/null || die "cloud-init failed: run '$0 ssh -- cloud-init status --long'"
}

down() {
  running || return 0
  log "Shutting the VM down"
  vm_ssh sudo systemctl poweroff 2>/dev/null || true
  local i
  for i in $(seq 1 30); do running || return 0; sleep 1; done
  kill "$(cat "$PIDFILE")" 2>/dev/null || true
}

install_build() {
  need go npm
  log "Building tilde (with the UI) and the e2e tests"
  local out="$DIR/build"
  mkdir -p "$out"
  make -s -C "$ROOT" web >/dev/null
  # Built here, not into bin/, so a running "make dev-server" is untouched.
  (cd "$ROOT/server" && CGO_ENABLED=1 go build -trimpath -o "$out/tilde" ./cmd/tilde)
  (cd "$ROOT/server" && go test -c -tags e2e -o "$out/e2e.test" ./e2e)
  # The package (D65), so the run tests it too: a newer version each run,
  # so each install is an upgrade, which restarts Tilde.
  rm -f "$out"/tilde_*.deb
  (cd "$ROOT" && TILDE_VERSION="0.0.0+e2e.$(date +%s)" TILDE_ARCH=amd64 TILDE_BIN="$out/tilde" \
    go run github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.47.0 package -f packaging/nfpm.yaml -p deb -t "$out/" >/dev/null)

  log "Installing the package into the VM"
  vm_scp "$out/e2e.test" "$out"/tilde_*.deb ubuntu@127.0.0.1:
  # The package's own files, every time: the two-factor setup below edits
  # /etc/pam.d/tilde, and confmiss puts back what is removed here. The rest
  # is what "make install" left in earlier VMs.
  vm_ssh 'set -e
    sudo rm -f /etc/pam.d/tilde /etc/default/tilde /usr/local/bin/tilde /etc/systemd/system/tilde.service
    wants=/etc/systemd/system/multi-user.target.wants/tilde.service
    [ -e $wants ] || sudo rm -f $wants
    sudo DEBIAN_FRONTEND=noninteractive dpkg -i --force-confnew,confmiss tilde_*.deb >/dev/null
    rm -f tilde_*.deb
    systemctl is-enabled --quiet tilde || { echo "the package did not enable tilde" >&2; exit 1; }
    # Installed over a Tilde that "make install" started, the package only
    # starts it, which leaves that one running; and a fresh server forgets
    # the login rate limit of earlier runs.
    sudo systemctl restart tilde
    for i in $(seq 1 20); do
      curl -ksf -o /dev/null https://127.0.0.1:8443/ && exit 0
      sleep 0.5
    done
    echo "tilde did not start" >&2
    sudo journalctl -u tilde --no-pager -n 50 >&2
    exit 1'

  # Two-factor for the second test account only (test VM only: Tilde ships
  # without it, D43). pam_oath goes after common-auth, as docs/security.md
  # says, so the password is asked first.
  vm_ssh "set -e
    dpkg -s libpam-oath oathtool libnotify-bin >/dev/null 2>&1 ||
      sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -q libpam-oath oathtool libnotify-bin >/dev/null
    id $TEST_2FA_USER >/dev/null 2>&1 || sudo useradd -m -s /bin/bash $TEST_2FA_USER
    echo '$TEST_2FA_USER:$TEST_PASS' | sudo chpasswd
    sudo sed -i '/^@include common-auth/a auth [success=1 default=ignore] pam_succeed_if.so user notin $TEST_2FA_USER quiet\\nauth required pam_oath.so usersfile=/etc/users.oath window=5' /etc/pam.d/tilde"

  # Lets the test user set up and mount loop images through udisks without
  # an active session, so the drives test has a stick mounted by the user.
  # From Tilde's web session, mounting asks for the user's own password
  # instead, as polkit asks an administrator's, so the tests go through
  # Tilde's polkit agent (D44). Test VM only.
  vm_ssh "sudo tee /etc/polkit-1/rules.d/49-tilde-e2e.rules >/dev/null" <<EOF
polkit.addRule(function (action, subject) {
  if (subject.user != "$TEST_USER") {
    return polkit.Result.NOT_HANDLED;
  }
  if (action.id == "org.freedesktop.udisks2.loop-setup") {
    return polkit.Result.YES;
  }
  if (action.id.indexOf("org.freedesktop.udisks2.filesystem-mount") == 0) {
    var type = "";
    try {
      type = polkit.spawn(["/usr/bin/loginctl", "show-session", subject.session, "-p", "Type", "--value"]).trim();
    } catch (e) {}
    return type == "web" ? polkit.Result.AUTH_SELF : polkit.Result.YES;
  }
  // The test system service, with the user's own password, through
  // Tilde's agent (D44); other services stay an administrator's.
  if (action.id == "org.freedesktop.systemd1.manage-units" &&
      action.lookup("unit") == "$TEST_SERVICE.service") {
    return polkit.Result.AUTH_SELF;
  }
  // Enabling and disabling at boot (D62): polkit is not told which unit.
  if (action.id == "org.freedesktop.systemd1.manage-unit-files") {
    return polkit.Result.AUTH_SELF;
  }
  // Network changes from the test user's web session; Wi-Fi on and off
  // asks for the user's own password, to go through Tilde's agent too.
  if (action.id.indexOf("org.freedesktop.NetworkManager.") == 0) {
    return action.id == "org.freedesktop.NetworkManager.enable-disable-wifi" ?
      polkit.Result.AUTH_SELF : polkit.Result.YES;
  }
  return polkit.Result.NOT_HANDLED;
});
EOF

  # Wi-Fi for the network applet (D55), all inside the VM: three radios
  # from mac80211_hwsim; hostapd serves the two test networks on wlan0 and
  # wlan1, with dnsmasq for DHCP (no router, so they never become the
  # default route), and NetworkManager manages only wlan2, so the VM's own
  # network (netplan and systemd-networkd) and ssh are left alone. Test VM
  # only.
  vm_ssh "set -e
    if ! dpkg -s network-manager hostapd dnsmasq-base iw >/dev/null 2>&1 || ! modinfo mac80211_hwsim >/dev/null 2>&1; then
      sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -q network-manager hostapd dnsmasq-base iw \
        linux-modules-extra-\$(uname -r) >/dev/null
    fi
    echo mac80211_hwsim | sudo tee /etc/modules-load.d/tilde-e2e.conf >/dev/null
    echo 'options mac80211_hwsim radios=3' | sudo tee /etc/modprobe.d/tilde-e2e.conf >/dev/null
    lsmod | grep -q '^mac80211_hwsim' || sudo modprobe mac80211_hwsim radios=3
    sudo mkdir -p /etc/tilde-e2e"
  vm_ssh "sudo tee /etc/NetworkManager/conf.d/99-tilde-e2e.conf >/dev/null" <<EOF
[keyfile]
unmanaged-devices=*,except:interface-name:wlan2
EOF
  vm_ssh "sudo tee /etc/tilde-e2e/ap-secure.conf >/dev/null" <<EOF
interface=wlan0
driver=nl80211
ssid=$TEST_WIFI_SECURE
hw_mode=g
channel=1
wpa=2
wpa_key_mgmt=WPA-PSK
rsn_pairwise=CCMP
wpa_passphrase=$TEST_WIFI_PASS
EOF
  vm_ssh "sudo tee /etc/tilde-e2e/ap-open.conf >/dev/null" <<EOF
interface=wlan1
driver=nl80211
ssid=$TEST_WIFI_OPEN
hw_mode=g
channel=6
EOF
  vm_ssh "sudo tee /etc/systemd/system/tilde-e2e-ap.service >/dev/null" <<EOF
[Unit]
Description=Wi-Fi access points for the e2e tests
After=network.target
[Service]
ExecStartPre=-/usr/sbin/ip addr add 10.42.1.1/24 dev wlan0
ExecStartPre=-/usr/sbin/ip addr add 10.42.2.1/24 dev wlan1
ExecStart=/usr/sbin/hostapd /etc/tilde-e2e/ap-secure.conf /etc/tilde-e2e/ap-open.conf
Restart=on-failure
[Install]
WantedBy=multi-user.target
EOF
  vm_ssh "sudo tee /etc/systemd/system/tilde-e2e-dhcp.service >/dev/null" <<EOF
[Unit]
Description=DHCP for the e2e test Wi-Fi networks
After=tilde-e2e-ap.service
[Service]
ExecStart=/usr/sbin/dnsmasq --keep-in-foreground --conf-file=/dev/null --port=0 --bind-dynamic \
  --interface=wlan0 --interface=wlan1 --dhcp-range=10.42.1.10,10.42.1.99,1h --dhcp-range=10.42.2.10,10.42.2.99,1h \
  --dhcp-option=3 --dhcp-option=6 --dhcp-leasefile=/run/tilde-e2e-dhcp.leases
Restart=on-failure
[Install]
WantedBy=multi-user.target
EOF
  # Services that do nothing, to start and stop (D60). The user one is
  # everyone's, in /etc/systemd/user, so the test user's manager has it.
  vm_ssh "sudo tee /etc/systemd/system/$TEST_SERVICE.service >/dev/null" <<EOF
[Unit]
Description=Tilde e2e test service
[Service]
ExecStart=/usr/bin/sleep infinity
[Install]
WantedBy=multi-user.target
EOF
  # It logs 300 lines (to scroll through in the journal) and then waits.
  # The unit gets "$$", systemd's "$"; each "\$" here is the heredoc's "$".
  vm_ssh "sudo tee /etc/systemd/user/$TEST_USER_SERVICE.service >/dev/null" <<EOF
[Unit]
Description=Tilde e2e test user service
[Service]
ExecStart=/bin/sh -c 'for i in \$\$(seq 1 300); do echo "line \$\$i"; done; exec sleep infinity'
EOF
  vm_ssh "set -e
    sudo systemctl daemon-reload
    sudo systemctl disable --now NetworkManager-wait-online.service >/dev/null 2>&1 || true
    sudo systemctl enable --now tilde-e2e-ap.service tilde-e2e-dhcp.service >/dev/null 2>&1
    sudo systemctl restart NetworkManager"

  # Mounted until the VM reboots; recreated by the next run after that.
  vm_ssh "set -e
    if ! mountpoint -q $TEST_DRIVE; then
      [ -f /var/tmp/tilde-drive.img ] || { sudo truncate -s 64M /var/tmp/tilde-drive.img; sudo mkfs.ext4 -q /var/tmp/tilde-drive.img; }
      sudo mkdir -p $TEST_DRIVE
      sudo mount -o loop /var/tmp/tilde-drive.img $TEST_DRIVE
      sudo chown $TEST_USER: $TEST_DRIVE
    fi"
}

# Starts each UI run with a fresh server: its login rate limit (5 failed
# attempts per address in 15 minutes) and the logins waiting on a second
# question live in memory, and the suite fails some on purpose, so runs
# close together would otherwise refuse each other's logins.
restart_tilde() {
  vm_ssh 'set -e
    sudo systemctl restart tilde
    for i in $(seq 1 20); do
      curl -ksf -o /dev/null https://127.0.0.1:8443/ && exit 0
      sleep 0.5
    done
    echo "tilde did not come back" >&2
    exit 1'
}

# pam_oath refuses a code it has seen, keeping the last one in its users
# file: start each run from a fresh file, so reruns can use the same codes.
reset_oath() {
  vm_ssh "echo 'HOTP/T30 $TEST_2FA_USER - $TEST_2FA_SECRET' | sudo install -m 600 /dev/stdin /etc/users.oath"
}

run_tests() {
  log "Running the e2e tests"
  reset_oath
  local status=0
  # The full output is kept in $DIR/last-test.log for flaky failures.
  mount_test_usb
  vm_ssh "TILDE_URL=https://127.0.0.1:8443 TILDE_USER=$TEST_USER TILDE_PASS=$TEST_PASS TILDE_USB=$TEST_USB \
    TILDE_2FA_USER=$TEST_2FA_USER TILDE_2FA_SECRET=$TEST_2FA_SECRET ./e2e.test -test.v -test.count=1" 2>&1 |
    tee "$DIR/last-test.log" || status=$?
  if [ "$status" -ne 0 ]; then
    log "Tests failed; tilde service log:"
    vm_ssh sudo journalctl -u tilde --no-pager -n 100 || true
  fi
  return "$status"
}

# Sets up the test "USB stick" as the test user and mounts it through
# udisks (at /media/$TEST_USER/$TEST_USB), as plugging one in would.
mount_test_usb() {
  vm_ssh "set -e
    img=/var/tmp/tilde-usb.img
    if [ ! -f \$img ]; then
      sudo truncate -s 32M \$img
      sudo mkfs.vfat -n $TEST_USB \$img >/dev/null
      sudo chown $TEST_USER: \$img
    fi
    dev=\$(sudo losetup -j \$img | cut -d: -f1 | head -n1)
    if [ -z \"\$dev\" ]; then
      dev=\$(sudo -u $TEST_USER udisksctl loop-setup --no-user-interaction -f \$img | grep -o '/dev/loop[0-9]*')
      udevadm settle
    fi
    findmnt -n \$dev >/dev/null || sudo -u $TEST_USER udisksctl mount --no-user-interaction -b \$dev >/dev/null"
}

# Starts the network test from Wi-Fi on, disconnected, with no saved
# Wi-Fi connections.
reset_wifi() {
  vm_ssh "set -e
    sudo nmcli radio wifi on
    sudo nmcli -t -f UUID,TYPE connection show | awk -F: '\$2 == \"802-11-wireless\" { print \$1 }' |
      xargs -r -n1 sudo nmcli connection delete uuid >/dev/null
    sudo nmcli device disconnect wlan2 >/dev/null 2>&1 || true
    sudo nmcli device wifi rescan ifname wlan2 >/dev/null 2>&1 || true"
}

# Starts the Services test with both test services stopped. The user's
# manager runs while the test user has a session; reload it so it knows
# the user service.
reset_services() {
  vm_ssh "sudo systemctl stop $TEST_SERVICE.service
    sudo systemctl disable $TEST_SERVICE.service 2>/dev/null
    uid=\$(id -u $TEST_USER)
    if [ -S /run/user/\$uid/bus ]; then
      sudo -u $TEST_USER XDG_RUNTIME_DIR=/run/user/\$uid systemctl --user daemon-reload || true
      sudo -u $TEST_USER XDG_RUNTIME_DIR=/run/user/\$uid systemctl --user stop $TEST_USER_SERVICE.service || true
    fi"
}

# The Playwright suite (web/e2e) in Chromium on the host, against the VM
# through its own SSH tunnel. Reports land in web/playwright-report.
run_ui_tests() {
  need npx
  [ -x "$ROOT/web/node_modules/.bin/playwright" ] || die "Playwright is not installed; run: make deps"
  log "Getting Playwright's Chromium (cached after the first run)"
  (cd "$ROOT/web" && npx playwright install chromium) >/dev/null ||
    die "could not install Chromium; try: cd web && npx playwright install --with-deps chromium"

  restart_tilde
  reset_oath
  mount_test_usb
  reset_wifi
  reset_services

  # The app port too (D76): browsers find it one port up.
  vm_tunnel -L "127.0.0.1:$UI_TEST_PORT:127.0.0.1:8443" -L "127.0.0.1:$((UI_TEST_PORT + 1)):127.0.0.1:8444" &
  local tunnel=$!
  # shellcheck disable=SC2064 # expand now: the pid is local
  trap "kill $tunnel 2>/dev/null" EXIT
  local i
  for i in $(seq 1 20); do
    curl -ksf -o /dev/null "https://127.0.0.1:$UI_TEST_PORT/" && break
    kill -0 "$tunnel" 2>/dev/null || die "the tunnel to the VM failed (is port $UI_TEST_PORT free?)"
    sleep 0.5
  done

  log "Running the UI tests"
  local status=0
  (cd "$ROOT/web" && TILDE_URL="https://localhost:$UI_TEST_PORT" TILDE_USER=$TEST_USER TILDE_PASS=$TEST_PASS \
    TILDE_DRIVE=$TEST_DRIVE TILDE_USB=$TEST_USB TILDE_2FA_USER=$TEST_2FA_USER TILDE_2FA_SECRET=$TEST_2FA_SECRET \
    TILDE_WIFI_SECURE=$TEST_WIFI_SECURE TILDE_WIFI_OPEN=$TEST_WIFI_OPEN TILDE_WIFI_PASS=$TEST_WIFI_PASS \
    TILDE_SERVICE=$TEST_SERVICE TILDE_USER_SERVICE=$TEST_USER_SERVICE \
    npx playwright test "$@") 2>&1 | tee "$DIR/last-ui-test.log" || status=$?
  kill "$tunnel" 2>/dev/null || true
  trap - EXIT
  if [ "$status" -ne 0 ]; then
    log "UI tests failed; traces and screenshots: cd web && npx playwright show-report"
  fi
  return "$status"
}

cmd="${1:-test}"
[ $# -gt 0 ] && shift
case "$cmd" in
  test)
    up
    install_build
    # Both suites run even if the first fails; either failing fails the run.
    status=0
    run_tests || status=$?
    run_ui_tests || status=$?
    exit "$status"
    ;;
  ui)
    running || die "the VM is not running; start it with: $0 up"
    [ "${1:-}" = "--" ] && shift
    run_ui_tests "$@"
    ;;
  up) up ;;
  fetch) fetch ;;
  ssh)
    [ "${1:-}" = "--" ] && shift
    running || die "the VM is not running; start it with: $0 up"
    if [ $# -gt 0 ]; then vm_ssh "$@"; else vm_ssh -t; fi
    ;;
  logs) vm_ssh sudo journalctl -u tilde --no-pager -n 200 ;;
  tunnel)
    running || die "the VM is not running; start it with: $0 up"
    log "Tilde in the VM: https://localhost:$UI_PORT (user $TEST_USER, password $TEST_PASS)"
    vm_ssh -N -L "127.0.0.1:$UI_PORT:127.0.0.1:8443" -L "127.0.0.1:$((UI_PORT + 1)):127.0.0.1:8444"
    ;;
  down) down ;;
  reset) down; rm -f "$DISK" "$SEED" "$PIDFILE"; log "VM disk deleted; the next run starts fresh" ;;
  *) die "unknown command '$cmd' (test, ui, up, fetch, ssh, logs, tunnel, down, reset)" ;;
esac
