## What & why
<!-- One task per PR. Link the issue: Closes #… -->

## Handoff
- **Agent:** <!-- claude / codex / gemini / cursor / human -->
- **Left to do / follow-ups:**
- **Risks / things reviewers should check:**

## Checklist
- [ ] `make check` passes
- [ ] Tests cover the new behavior
- [ ] AGENTS.md invariants hold (or a new ADR in `docs/decisions/` explains the change)
- [ ] README / AGENTS.md updated if public API or rules changed
- [ ] `make bench` results below if `router.go` / `context.go` / `kite.go` hot path changed
