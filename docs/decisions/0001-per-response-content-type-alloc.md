# 0001 — Pay one alloc per response for Content-Type

**Status:** Accepted (2026-10-03)

**Context:** Response helpers used to assign shared global `[]string` values
into the header map to stay at 0 allocs. Any code that mutated
`Header()["Content-Type"][0]` changed every concurrent response. A per-`Ctx`
reusable slice is also unsafe: net/http may read the header map after
`ServeHTTP` returns, when the `Ctx` is already back in the pool.

**Decision:** Set Content-Type with `Header().Set(k, MIME...)`.

**Consequences:** +1 alloc and ~20 ns per `String`/`JSON`/`HTML`/`Bytes`
response. Routing stays 0 allocs (`TestZeroAlloc`). Do not reintroduce shared
or pooled header slices to win the alloc back.
