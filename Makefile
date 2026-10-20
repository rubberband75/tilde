# Tilde build. Requirements: Go 1.24+, Node 22+, gcc, libpam0g-dev.
#
#   make deps         install frontend dependencies
#   make build        frontend + server binary with the UI embedded (bin/tilde)
#   make dev          print how to run the two dev servers
#   make test         Go vet + tests, frontend type check
#   make e2e          end-to-end tests against a running server (use a VM)
#   make install      install binary, PAM file and systemd unit (sudo)

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BIN     := bin/tilde
WEBUI   := server/internal/webui/dist
LDFLAGS := -s -w -X tilde/internal/buildinfo.Version=$(VERSION)
DEV_LISTEN ?= 127.0.0.1:8443

.PHONY: all build deps web server server-nopam test test-go test-web check \
        dev dev-server dev-server-root dev-web icons e2e install uninstall clean

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
	cd server && go vet ./... && go test -race ./...

test-web:
	cd web && npm run typecheck

dev:
	@echo "Terminal 1:  make dev-server        (single-user: only your account can log in)"
	@echo "         or: make dev-server-root   (any account; needs sudo)"
	@echo "Terminal 2:  make dev-web           then open http://localhost:5173"

# Plain HTTP on loopback for the Vite proxy. Without root, Tilde runs in
# single-user mode: it checks your own password through PAM and starts the
# worker as you, without opening a logind session.
dev-server: server
	./$(BIN) serve --dev --listen $(DEV_LISTEN)

dev-server-root: server
	sudo ./$(BIN) serve --dev --listen $(DEV_LISTEN)

dev-web:
	cd web && npm run dev

icons:
	./scripts/fetch-icons.sh

# Needs a running server and a throwaway account, e.g. in a VM:
#   make e2e TILDE_URL=http://127.0.0.1:8443 TILDE_USER=tester TILDE_PASS=secret
e2e:
	cd server && TILDE_URL=$(TILDE_URL) TILDE_USER=$(TILDE_USER) TILDE_PASS=$(TILDE_PASS) \
		go test -tags e2e ./e2e/ -v -count=1

install: build
	sudo install -D -m 0755 $(BIN) /usr/local/bin/tilde
	sudo install -D -m 0644 packaging/pam.d/tilde /etc/pam.d/tilde
	sudo install -D -m 0644 packaging/systemd/tilde.service /etc/systemd/system/tilde.service
	[ -f /etc/default/tilde ] || sudo install -D -m 0644 packaging/default/tilde /etc/default/tilde
	sudo systemctl daemon-reload
	@echo "Installed. Start with: sudo systemctl enable --now tilde"

uninstall:
	-sudo systemctl disable --now tilde
	sudo rm -f /usr/local/bin/tilde /etc/pam.d/tilde /etc/systemd/system/tilde.service
	sudo systemctl daemon-reload

clean:
	rm -rf bin web/dist
	find $(WEBUI) -mindepth 1 ! -name .gitkeep -exec rm -rf {} +
