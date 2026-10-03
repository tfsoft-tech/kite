# Kite 🪁

> Developed by **TF Soft Co., Ltd.** · https://github.com/tfsoft-tech/kite

A small, fast, resource-light web framework for Go, built purely on `net/http` with zero external dependencies.

- Radix-tree router with zero allocations (0 allocs) on every route type: static, `:param`, and `*wildcard`
- `Ctx` and the ResponseWriter wrapper are pooled, so the hot path produces no garbage
- Middleware is composed once at route registration, so there is no chain walk per request
- Handlers return `error`, and a central `ErrorHandler` formats the response without leaking internal error messages to clients
- Fully compatible with `http.Server`, HTTP/2, TLS, `httptest`, and existing `http.Handler`s
- Around 900 lines of core code — readable in an hour

## Installation

```bash
go get github.com/tfsoft-tech/kite@latest
```

## Getting Started

```go
app := kite.New()
app.Use(kite.Recover(), kite.RequestID(), kite.Logger())

app.GET("/users/:id", func(c *kite.Ctx) error {
    return c.JSON(map[string]string{"id": c.Param("id")})
})

api := app.Group("/api/v1", kite.Timeout(5*time.Second))
api.POST("/todos", createTodo)

app.Run(":8080") // graceful shutdown on SIGINT/SIGTERM
```

A full example lives in `examples/todo` (run it with `go run ./examples/todo`).

### Core API

| Area | Functions |
|---|---|
| Routing | `GET/POST/PUT/PATCH/DELETE/Handle`, `Group`, `Static`, `WrapHandler` |
| Request | `Param`, `ParamInt`, `Query`, `Header`, `Bind` (requires `Content-Type: application/json`, one JSON value, body size limit), `Set/Get`, `Route`, `Copy` |
| Response | `JSON`, `String`, `HTML`, `Bytes`, `Stream`, `Status`, `NoContent`, `Redirect` |
| Errors | `kite.NewError(code, msg)`, `ErrNotFound`, `ErrUnauthorized`, ... |
| Middleware | `Recover`, `Logger` (slog), `RequestID`, `CORS`, `Secure` (nosniff, X-Frame-Options, Referrer-Policy, optional CSP/HSTS), `Timeout` |
| Config | `BodyLimit`, `ErrorHandler`, `NotFound`, `JSONMarshal` (plug in sonic/go-json), `DisableJSONHTMLEscape`, `RedirectTrailingSlash`, timeouts |

Other features: 405 responses with an `Allow` header, HEAD automatically served by the GET handler, and safe server timeout defaults out of the box.

Secure defaults: `c.JSON` escapes `<`, `>` and `&`; `Bind` rejects non-JSON content types (415) and trailing data (400); `Static` never lists directories; `RequestID` only trusts client IDs of 1–64 `[A-Za-z0-9_-]` characters; trailing-slash redirects never point to `//host`.

## Benchmarks

> These numbers predate the security hardening. Setting `Content-Type` per response now costs one extra allocation (and ~20 ns) on responses written with `String`/`JSON`/`HTML`/`Bytes`; routing itself is still 0 allocs.

Measured on a 2 vCPU machine with Go 1.24.7, against Gin v1.10.1, Echo v4.13.3, httprouter v1.3.0, Chi v5.3.2, and Go's own `http.ServeMux`.
All routers use the same 189-route GitHub API set and were verified to match every route (median of 3 runs).

### Pure routing (ns/op, lower is better)

| Benchmark | **Kite** | Gin | Echo | httprouter | Chi | ServeMux |
|---|---|---|---|---|---|---|
| Static `/user/repos` | **54** | 61 | 80 | 45 | 410 | 170 |
| 1 param | **65** | 61 | 83 | 94 | 683 | 222 |
| 4 params | **100** | 100 | 151 | 155 | 871 | 609 |
| All 189 routes | **15,421** | 16,349 | 23,722 | 21,035 | 149,826 | 83,412 |
| allocs across 189 routes | **0** | 0 | 0 | 153 | 684 | 306 |

### Read a param and respond with JSON

| | **Kite** | Gin | Echo | Chi | ServeMux |
|---|---|---|---|---|---|
| ns/op | **284** | 334 | 383 | 921 | 472 |
| allocs/op | **1** | 3 | 2 | 6 | 3 |

### Real HTTP load (GOMAXPROCS=1, 64 connections, average of 2 runs)

| | **Kite** | Gin | Echo | Chi | ServeMux |
|---|---|---|---|---|---|
| req/s | **50,965** | 43,849 | 45,970 | 43,623 | 46,006 |
| Idle RAM | **6.7 MB** | 11.1 MB | 6.9 MB | 6.9 MB | 7.1 MB |
| Peak RAM under load | 13.3 MB | 16.3 MB | 13.3 MB | 13.8 MB | 13.5 MB |
| Binary size | 5.8 MB | 8.2 MB | 6.1 MB | 6.0 MB | 5.8 MB |

### Reading the results honestly

- Kite is on par with Gin, among the fastest, and wins on the all-routes and JSON-response benchmarks. httprouter is still slightly faster on a single static route.
- In real requests, most of the cost is in `net/http` and the network, so the gap between frameworks shrinks to roughly 10–15%, and these numbers vary by machine.
- Idle RAM and binary size are close to the stdlib because Kite has no dependencies at all.
- If you need dramatically more speed, the next step is swapping the underlying layer from `net/http` to `fasthttp`, or using a faster JSON encoder (`Config.JSONMarshal`) — at the cost of compatibility with the `net/http` ecosystem.

Reproduce with:

```bash
cd bench && GOFLAGS=-mod=mod go test -bench . -count 3
```

## Caveats

- Do not keep a `*kite.Ctx` after the handler returns or pass it to another goroutine — the object is reused. Copy out the values you need, or call `c.Copy()` inside the handler for a detached copy (its request context is not canceled when the request ends, and it cannot write a response).
- `CORS` panics if `AllowOrigins` contains `"*"` together with `AllowCredentials: true`; list trusted origins explicitly.
- `app.Use` must be called before registering routes (calling it afterwards panics so the mistake isn't silent).
- Conflicting routes such as `/a/:id` and `/a/:name` panic at startup, not at request time.
- Current status is prototype: all unit tests and race tests pass, but it has not yet been proven in production.

## License

[MIT](LICENSE) © 2026 TF Soft Co., Ltd.
