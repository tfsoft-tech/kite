# 0002 — Secure defaults; loosening is opt-in

**Status:** Accepted (2026-10-03)

**Context:** Security hardening (commit e16b907) changed defaults that trade
a little speed or convenience for safety.

**Decision:** These are the defaults and must not be weakened by default:

- `c.JSON` escapes `<`, `>`, `&` (opt-out: `Config.DisableJSONHTMLEscape`).
- `Bind` requires `Content-Type: application/json` (415) and exactly one JSON value (400).
- `CORS` panics on `AllowOrigins: ["*"]` with `AllowCredentials`.
- `Static` never lists directories without `index.html`.
- Trailing-slash redirects never target `//…` or `/\…`.
- `RequestID` accepts only 1–64 chars of `[A-Za-z0-9_-]`.
- Pooled `Ctx` is cleared in `release()`; `Copy()` for goroutines.

**Consequences:** New options that relax any of these must be explicit
`Config` fields defaulting to the safe behavior, with tests for both.
