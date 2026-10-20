---
name: next-task
description: Pick up the next piece of Tilde work from docs/roadmap.md, plan it against the architecture and decisions, implement it with tests, and update the docs. Use when asked to "continue", "do the next thing" or work on a roadmap item.
---

# Next task

1. Read `docs/roadmap.md`. Unless the user named a task, take the first
   unchecked item in the lowest phase. Say which one you picked and why.
2. Read what it touches:
   - `docs/architecture.md` for where the code goes ("Where to add things").
   - `docs/decisions.md` for decisions that constrain it. If the task needs
     an open decision settled, ask the user before writing code.
   - `docs/security.md` if it touches login, cookies, the server, the helper,
     file serving or anything that runs commands.
3. Plan briefly: files to change, API changes, tests. For anything crossing
   the server/helper/worker boundary, check the rules in `CLAUDE.md`.
4. Implement in small steps:
   - Worker endpoints: handler in `server/internal/worker`, route in
     `worker.go`, a test in `worker_test.go` over the yamux pipe.
   - Frontend: types in `web/src/api/types.ts`, calls in `web/src/api/`,
     UI in the app or shell component, theme tokens only.
5. Verify: `make test`, and for UI changes `make build` (and look at it with
   `make dev-server` + `make dev-web` if you can).
6. Update the docs in the same change:
   - tick the item in `docs/roadmap.md` (or split it if only part is done),
   - `docs/api.md` for API changes,
   - a new `docs/decisions.md` entry for any decision made along the way.
7. Summarize what changed and what the user should try by hand (especially
   anything that needs root or a real logind session, which only the VM e2e
   run can check).
