# Tilde

A browser-based Linux desktop. Tilde looks and works like Cinnamon on Linux
Mint (Mint-Y Dark), but runs in a web page: log in with an account on the
host, and the desktop, file manager and terminals act as that Linux user.

It is not remote screen sharing. The browser draws the desktop; the host
runs a small Go server that authenticates you through PAM and starts a
per-user worker process, so the kernel enforces your normal file and process
permissions.

> Status: early skeleton (v0.1). Login, the desktop, window manager, panel,
> start menu, a Nemo-style file manager and persistent terminals work. See
> [docs/roadmap.md](docs/roadmap.md) for what is next.

## What works today

- Login with host accounts through PAM (`/etc/pam.d/tilde`); each login gets
  a real logind session of type `web`. Root login is disabled; use sudo.
- Bottom panel with a Cinnamon-style menu, launchers, window list and clock.
- Windows you can drag, resize, minimize, maximize and close. The layout is
  restored after a page reload.
- Desktop icons for Computer, Home, Trash and the files in your Desktop
  folder.
- Files: browse the whole filesystem, icon and list views, places sidebar,
  new folder, rename, move to trash, restore, empty trash, upload (drag and
  drop), download. The trash follows the freedesktop spec, so it is shared
  with the real Nemo.
- Terminal: a real shell on a PTY in your session. It keeps running when you
  close the browser, and reattaches with recent output after a reload.

## Requirements

- Linux with PAM and systemd (developed for Linux Mint 22 / Ubuntu 24.04)
- Go 1.24+ (`go.mod` will fetch a newer toolchain automatically if needed)
- Node.js 22+ (the Mint/Ubuntu `nodejs` package is too old; use nvm or NodeSource)
- `build-essential` and `libpam0g-dev` (PAM needs cgo)

```bash
sudo apt install build-essential libpam0g-dev git
```

## Quick start (development)

```bash
make deps          # frontend dependencies
make dev-server    # terminal 1: Go server on 127.0.0.1:8443 (plain HTTP, loopback only)
make dev-web       # terminal 2: Vite dev server
```

Open http://localhost:5173 and log in with your own account. Without root the
server runs in single-user mode: only the account that started it can log
in. Use `make dev-server-root` to try other accounts.

## Install

```bash
make install                      # binary, PAM file, systemd unit
sudo systemctl enable --now tilde
```

Tilde listens on `127.0.0.1:8443` with a self-signed certificate by default
(the fingerprint is in `journalctl -u tilde`). Edit `/etc/default/tilde` to
listen on a VPN or LAN address. Exposing it to the public internet is not
recommended; see [docs/security.md](docs/security.md).

## Documentation

- [Architecture](docs/architecture.md)
- [Decisions](docs/decisions.md)
- [Roadmap](docs/roadmap.md)
- [Security](docs/security.md)
- [API](docs/api.md)
- [Development and testing](docs/development.md)
- [Theming](docs/theming.md)
- [Lessons from webdesktopd](docs/lessons-from-webdesktopd.md)

## License

Tilde is licensed under the GNU Affero General Public License v3.0 or later
([LICENSE](LICENSE)). If you run a modified version for other people, you must
offer them its source.

The bundled Mint-Y icons (`web/public/icons/Mint-Y`) come from
[linuxmint/mint-y-icons](https://github.com/linuxmint/mint-y-icons) and are
licensed CC BY-SA 4.0, with some icons under GPLv3; see the license and
credits files in that directory.
