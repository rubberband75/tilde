---
name: verify
description: Run Tilde's full set of checks before calling work done - Go vet and race tests, frontend type check and build, and (when possible) a manual UI smoke test. Use after making changes or when asked to check that everything still works.
---

# Verify

1. `make test` — go vet, `go test -race ./...` in `server/`, `vue-tsc` in `web/`.
   Fix failures; do not skip or delete tests to make them pass.
2. `make build` — builds the frontend, embeds it and compiles `bin/tilde`
   with PAM. If libpam headers are missing, say so and use `make server-nopam`
   to at least check compilation.
3. `gofmt -l server/` must print nothing.
4. If the change touches the UI and you can run servers: `make dev-server`
   and `make dev-web`, open http://localhost:5173, log in, and exercise the
   change (open/drag/resize windows, Files operations, a terminal command,
   reload the page and check the layout and terminals come back).
5. If the change touches login, the helper, PAM, sessions or privilege
   dropping, tell the user to run the e2e suite in their VM
   (`docs/development.md`); it cannot be checked meaningfully elsewhere.
6. Report what passed, what you could not run, and anything that looked off.
