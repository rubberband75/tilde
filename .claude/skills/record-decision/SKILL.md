---
name: record-decision
description: Record a design decision for Tilde in docs/decisions.md (and CLAUDE.md if it changes a rule). Use when the user decides something about architecture, security, UX, naming, licensing or tooling, or says "let's go with X".
---

# Record a decision

1. Read `docs/decisions.md` to find the next number and any related entry.
2. Append an entry at the end of the numbered list, before "Open decisions":

   ```
   **Dnn — short title** (YYYY-MM-DD, accepted)
   What was decided, in one or two sentences. Why. What was rejected and why.
   ```

   Use today's date. Keep the user's reasoning; do not invent reasons they
   did not give.
3. If it replaces an earlier decision, change that entry's status to
   "superseded by Dnn". Never delete old entries.
4. If it settles a row in the "Open decisions" table, remove the row.
5. If it changes one of the rules in `CLAUDE.md` or `docs/security.md`,
   update those too and point out the change to the user.
6. If it creates or removes work, update `docs/roadmap.md`.
