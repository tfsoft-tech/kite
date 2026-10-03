# AGENTS.md

Single source of truth for every AI agent (Claude Code, Codex, Gemini CLI,
Cursor, Copilot, ...) and human working on Kite. Tool-specific files
(`CLAUDE.md`, `GEMINI.md`) only import this file — put rules **here**, never
in them.

## Project

Kite is a small, zero-dependency Go web framework on `net/http`
(`github.com/tfsoft-tech/kite`). Core is ~1,200 lines in the repo root.

| File | Owns |
|---|---|
| `router.go` | radix tree, `find` (hot path), 405 `Allow` |
| `kite.go` | `App`, `Config`, `ServeHTTP`, groups, `Static`, trailing-slash redirect, server |
| `context.go` | pooled `Ctx`, request/response helpers, `Bind`, `JSON`, `Copy` |
| `middleware.go` | `Recover`, `Logger`, `RequestID`, `CORS`, `Secure`, `Timeout` |
| `errors.go` | `HTTPError`, `DefaultErrorHandler` |
| `bench/` | separate module comparing Kite with Gin/Echo/Chi/httprouter/ServeMux |
| `examples/todo` | runnable example |
| `docs/decisions/` | decision records (ADRs) — read before changing anything they cover |

## Commands

```bash
make check   # gofmt + vet + race tests + agent-file check — must pass before every commit
make bench   # benchmark suite (slow; run when touching router.go / context.go / kite.go)
```

## Invariants (do not break without a new ADR)

1. **Zero dependencies** in the root module. `go.mod` stays `go 1.22`; don't use newer language/stdlib features.
2. **Routing is 0 allocs.** `TestZeroAlloc` enforces it. Response helpers cost exactly 1 alloc (Content-Type) — see ADR 0001.
3. **Pooled `Ctx`:** anything added to `Ctx` that holds a reference must be cleared in `release()`. Never let a `Ctx` escape the handler; use `Copy()`.
4. **No shared mutable globals** reachable from a request (e.g. shared header slices). See ADR 0001.
5. **Security defaults never get weaker** (ADR 0002): HTML-escaped JSON, `Bind` requires `application/json` and one value, no directory listings or dotfiles in `Static`, global middleware on 404/405/redirects, no `//host` redirects, validated request IDs, CORS `*`+credentials panics. Loosening is opt-in through `Config`.
6. **Misconfiguration panics at startup**, never silently at request time.
7. Handlers return `error`; internal error text never reaches the client.

## Workflow for multiple agents

- **One task = one branch = one PR.** Branch name: `ai/<agent>/<topic>` (e.g. `ai/claude/cors-vary`, `ai/codex/router-fuzz`). Humans: `<name>/<topic>`.
- **Never push straight to `main`** once branch protection is on; CI (`make check`) is the shared gate for every agent.
- **Claim work before starting:** comment on / assign the GitHub issue, so two agents don't do the same task. Skip tasks already claimed or with an open PR.
- **Before you start:** `git pull`, read this file and any ADR touching your files.
- **Small diffs.** Don't reformat, rename, or "clean up" code outside your task — other agents are working on other branches.
- **Keep docs in sync in the same PR:** public API change → `README.md`; new rule → this file; trade-off or reversal → new ADR in `docs/decisions/`.
- **Don't edit benchmark numbers in README** unless you re-ran `make bench` on the documented setup; otherwise add a note.
- **Commits:** imperative subject ≤ 72 chars, body says *why*, and a trailer naming the agent, e.g. `Co-Authored-By: Claude <noreply@anthropic.com>`.
- **Handoff:** the PR description is the handoff log — fill in the template (what changed, why, what's left, risks). Don't keep notes in untracked files.

## Definition of done

- [ ] `make check` passes locally
- [ ] Tests added for new behavior (and for each acceptance criterion of the task)
- [ ] Invariants above hold; if one changed, an ADR explains it
- [ ] README / AGENTS.md / ADRs updated where affected
- [ ] `make bench` run and results noted in the PR if hot-path files changed
