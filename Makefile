# Tilde build. Requirements: Go 1.24+, Node 22+, gcc, libpam0g-dev.
#
#   make deps         install frontend dependencies
#   make build        frontend + server binary with the UI embedded (bin/tilde)
#   make dev          print how to run the two dev servers
#   make test         gofmt/Prettier checks, Go vet + tests, frontend type check + tests
#   make fmt          format Go and frontend code
#   make e2e-vm       end-to-end tests in a disposable QEMU VM (scripts/e2e-vm.sh)
#   make e2e-pi PI=user@host   a part of them on a spare Raspberry Pi (scripts/e2e-pi.sh)
#   make e2e          end-to-end tests against a running server (use a VM)
#   make e2e-ui       Playwright UI tests against a running server (use a VM)
#   make deb          a .deb of the build in dist/ (nfpm, D65)
#   make install      install binary, PAM file and systemd unit (sudo)

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BIN     := bin/tilde
WEBUI   := server/internal/webui/dist
LDFLAGS := -s -w -X tilde/internal/buildinfo.Version=$(VERSION)
DEV_LISTEN ?= 127.0.0.1:8443

# Packages (D65): the version from a vX.Y.Z tag (commits since it as
# +N.gHASH), else 0.0.0+gitDATE.HASH; the architecture of the build.
PKG_VERSION ?= $(or $(shell git describe --tags --match 'v[0-9]*' 2>/dev/null | sed -e 's/^v//' -e 's/-\([0-9]*\)-g/+\1.g/'),0.0.0+git$(shell date -u +%Y%m%d).$(shell git rev-parse --short HEAD))
PKG_ARCH    ?= $(shell cd server && go env GOARCH)
NFPM        := go run github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.47.0

.PHONY: all build deps web server server-nopam test test-go test-web check \
        fmt deb e2e-vm e2e-pi dev dev-server dev-server-root dev-web icons fonts e2e e2e-ui install uninstall clean

all: build

build: web server

deps:
	cd web && npm ci

# Build the frontend and copy it where the server embeds it.
web:
	cd web && npm run build
	find $(WEBUI) -mindepth 1 ! -name .gitkeep -exec rm -rf {} +
	cp -r web/dist/. $(WEBUI)/

# The real build: cgo is required for PAM.
server:
	mkdir -p bin
	cd server && CGO_ENABLED=1 go build -trimpath -ldflags "$(LDFLAGS)" -o ../$(BIN) ./cmd/tilde

# Builds anywhere (no libpam); every login fails. Useful for CI and tests.
server-nopam:
	mkdir -p bin
	cd server && CGO_ENABLED=0 go build -tags nopam -trimpath -ldflags "$(LDFLAGS)" -o ../$(BIN) ./cmd/tilde

test: test-go test-web
check: test

test-go:
	@unformatted=$$(gofmt -l server); if [ -n "$$unformatted" ]; then \
		echo "gofmt needed (run make fmt):"; echo "$$unformatted"; exit 1; fi
	cd server && go vet ./... && go test -race ./...

test-web:
	cd web && npm run format:check && npm run typecheck && npm test

fmt:
	gofmt -w server
	cd web && npm run format

dev:
	@echo "Terminal 1:  make dev-server        (single-user: only your account can log in)"
	@echo "         or: make dev-server-root   (any account; needs sudo)"
	@echo "Terminal 2:  make dev-web           then open http://localhost:5173"

# Plain HTTP on loopback for the Vite proxy. Without root, Tilde runs in
# single-user mode: it checks your own password through PAM and starts the
# worker as you, without opening a logind session.
# Local web apps on the next port (D76); the page comes from Vite, so the
# server is told where browsers reach that port.
DEV_APPS_ORIGIN ?= http://localhost:8444

dev-server: server
	./$(BIN) serve --dev --listen $(DEV_LISTEN) --apps-origin $(DEV_APPS_ORIGIN)

dev-server-root: server
	sudo ./$(BIN) serve --dev --listen $(DEV_LISTEN) --apps-origin $(DEV_APPS_ORIGIN)

dev-web:
	cd web && npm run dev

icons:
	./scripts/fetch-icons.sh

# Rebuilds the bundled Ubuntu font and its @font-face rules (D56).
fonts:
	python3 scripts/build-fonts.py
	cd web && npx prettier --write src/styles/fonts.css >/dev/null

# Boots (or reuses) a throwaway VM, installs the current build like
# "make install" and runs the Go e2e suite there, then the Playwright UI
# suite (web/e2e) in a browser on this machine. See docs/development.md.
e2e-vm:
	./scripts/e2e-vm.sh test

# On a Raspberry Pi you can wipe, marked with /etc/tilde-e2e-disposable
# (D74); only when asked for, not part of e2e-vm.
e2e-pi:
	@[ -n "$(PI)" ] || { echo "usage: make e2e-pi PI=user@host (see scripts/e2e-pi.sh)" >&2; exit 2; }
	./scripts/e2e-pi.sh $(PI) test

# Needs a running server and a throwaway account, e.g. in a VM:
#   make e2e TILDE_URL=http://127.0.0.1:8443 TILDE_USER=tester TILDE_PASS=secret
e2e:
	cd server && TILDE_URL=$(TILDE_URL) TILDE_USER=$(TILDE_USER) TILDE_PASS=$(TILDE_PASS) \
		go test -tags e2e ./e2e/ -v -count=1

# The UI suite alone, e.g. against the VM's tunnel:
#   make e2e-ui TILDE_URL=https://localhost:9443 TILDE_USER=tester TILDE_PASS=tester-pass
e2e-ui:
	cd web && TILDE_URL=$(TILDE_URL) TILDE_USER=$(TILDE_USER) TILDE_PASS=$(TILDE_PASS) npx playwright test

# Packages what "make build" made: bin/tilde must be for PKG_ARCH.
deb: build
	mkdir -p dist
	TILDE_VERSION=$(PKG_VERSION) TILDE_ARCH=$(PKG_ARCH) TILDE_BIN=$(BIN) $(NFPM) package -f packaging/nfpm.yaml -p deb -t dist/

# Without a package: under /usr/local, the unit pointed there.
install: build
	sudo install -D -m 0755 $(BIN) /usr/local/bin/tilde
	sudo install -D -m 0644 packaging/pam.d/tilde /etc/pam.d/tilde
	sed 's|/usr/bin/tilde|/usr/local/bin/tilde|' packaging/systemd/tilde.service | \
		sudo install -D -m 0644 /dev/stdin /etc/systemd/system/tilde.service
	[ -f /etc/default/tilde ] || sudo install -D -m 0644 packaging/default/tilde /etc/default/tilde
	sudo systemctl daemon-reload
	@echo "Installed. Start with: sudo systemctl enable --now tilde"

uninstall:
	-sudo systemctl disable --now tilde
	sudo rm -f /usr/local/bin/tilde /etc/pam.d/tilde /etc/systemd/system/tilde.service
	sudo systemctl daemon-reload

clean:
	rm -rf bin dist web/dist
	find $(WEBUI) -mindepth 1 ! -name .gitkeep -exec rm -rf {} +
