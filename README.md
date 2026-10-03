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

## Quick Start

```go
package main

import (
	"log"
	"net/http"
	"time"

	"github.com/tfsoft-tech/kite"
)

type User struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func main() {
	app := kite.New(kite.Config{
		BodyLimit:             1 << 20, // max request body for Bind (default 4 MiB)
		RedirectTrailingSlash: true,    // /users/ -> /users
	})

	// Global middleware: call Use BEFORE registering any route.
	app.Use(
		kite.Recover(),   // panic -> 500
		kite.RequestID(), // X-Request-ID
		kite.Logger(),    // one slog line per request
		kite.Secure(),    // nosniff, X-Frame-Options, Referrer-Policy
		kite.CORS(kite.CORSConfig{AllowOrigins: []string{"https://app.example.com"}}),
	)

	app.GET("/health", func(c *kite.Ctx) error { return c.String("ok") })

	api := app.Group("/api/v1", kite.Timeout(5*time.Second))

	api.GET("/users/:id", func(c *kite.Ctx) error {
		id, err := c.ParamInt("id") // not a number -> 400
		if err != nil {
			return err
		}
		if id != 1 {
			return kite.NewError(http.StatusNotFound, "user not found")
		}
		return c.JSON(User{ID: id, Name: "Ann"})
	})

	api.POST("/users", func(c *kite.Ctx) error {
		var in User
		if err := c.Bind(&in); err != nil { // 415 / 400 / 413 handled for you
			return err
		}
		return c.Status(http.StatusCreated).JSON(in)
	})

	app.Static("/assets", "./public")

	if err := app.Run(":8080"); err != nil { // graceful shutdown on SIGINT/SIGTERM
		log.Fatal(err)
	}
}
```

```bash
curl localhost:8080/api/v1/users/1
# {"id":1,"name":"Ann"}

curl -X POST localhost:8080/api/v1/users \
  -H 'Content-Type: application/json' -d '{"id":2,"name":"Bo"}'
# 201 {"id":2,"name":"Bo"}
```

A fuller example lives in `examples/todo` (run it with `go run ./examples/todo`).

## Usage

### Routing

Register handlers with `GET`, `POST`, `PUT`, `PATCH`, `DELETE`, or `Handle(method, path, h)` for any other method. `HEAD` falls back to the `GET` handler, and a path that exists under another method gets `405` with an `Allow` header.

| Pattern | Matches | Read with |
|---|---|---|
| `/users` | exactly that path | — |
| `/users/:id` | one path segment | `c.Param("id")`, `c.ParamInt("id")` |
| `/files/*path` | everything after `/files/` | `c.Param("path")` |

When several patterns could match, static beats `:param`, which beats `*wildcard`. Conflicting routes such as `/a/:id` and `/a/:name` panic at startup.

Groups share a prefix and middleware, and can be nested:

```go
api := app.Group("/api", authMiddleware)
v1 := api.Group("/v1")
v1.GET("/me", meHandler) // GET /api/v1/me, runs authMiddleware
```

### Request

| Method | Returns |
|---|---|
| `c.Param(name)` / `c.ParamInt(name)` | path parameter (`ParamInt` returns a 400 error if not a number) |
| `c.Query(name)` | query-string value |
| `c.Header(name)` | request header |
| `c.Bind(&v)` | decodes a JSON body; requires `Content-Type: application/json` (415), exactly one JSON value (400), at most `BodyLimit` bytes (413), and rejects unknown fields |
| `c.Method()`, `c.Path()`, `c.Route()` | method, path, matched pattern (e.g. `/users/:id`) |
| `c.Set(k, v)` / `c.Get(k)` | request-scoped values, e.g. the current user set by auth middleware |
| `c.Request` | the underlying `*http.Request` |

### Response

| Method | Writes |
|---|---|
| `c.JSON(v)` | JSON (`<`, `>`, `&` escaped) |
| `c.String(s)`, `c.HTML(s)` | text / HTML |
| `c.Bytes(contentType, b)`, `c.Stream(contentType, r)` | raw bytes / an `io.Reader` |
| `c.NoContent(code)`, `c.Redirect(code, url)` | status only / redirect |
| `c.Status(code)` | sets the status for the next write; chainable: `c.Status(201).JSON(v)` |
| `c.SetHeader(k, v)` | sets a response header |

### Errors

Handlers return `error`; the central `ErrorHandler` turns it into a response:

- `kite.NewError(code, msg)` → that status with `{"error": "msg"}`.
- Predefined: `kite.ErrNotFound`, `ErrMethodNotAllowed`, `ErrUnauthorized`, `ErrForbidden`, `ErrInternal`.
- Any other error → generic `500`; the real message is logged, never sent to the client.

Customize the format with `Config.ErrorHandler` and the 404 response with `Config.NotFound`.

### Middleware

Built in:

| Middleware | Does |
|---|---|
| `Recover()` | turns panics into `500` and logs the stack |
| `Logger()` | one structured `log/slog` line per request |
| `RequestID()` | propagates a valid client `X-Request-ID` or generates one; stored as `c.Get("request_id")` |
| `Secure(cfg ...SecureConfig)` | `X-Content-Type-Options`, `X-Frame-Options`, `Referrer-Policy`; optional CSP and HSTS |
| `CORS(CORSConfig{...})` | CORS headers and preflight responses |
| `Timeout(d)` | cancels `c.Request.Context()` after `d`; returns `503` if the handler wrote nothing |

Writing your own is a function that wraps the next handler:

```go
func Auth(next kite.Handler) kite.Handler {
	return func(c *kite.Ctx) error {
		if c.Header("Authorization") == "" {
			return kite.ErrUnauthorized
		}
		c.Set("user", "ann")
		return next(c)
	}
}

app.Use(Auth)                      // every request
admin := app.Group("/admin", Auth) // one group
app.GET("/me", meHandler, Auth)    // one route
```

Global middleware (`app.Use`) must be registered before any route and also runs for 404/405 responses and trailing-slash redirects. Group and route middleware run only on matched routes.

### Configuration

All fields of `kite.Config` are optional:

| Field | Default | Purpose |
|---|---|---|
| `BodyLimit` | 4 MiB | max body size read by `Bind` |
| `ErrorHandler` | `DefaultErrorHandler` | formats returned errors |
| `NotFound` | `{"error":"not found"}` | handler for unmatched paths |
| `JSONMarshal` | `encoding/json` | plug in a faster encoder (sonic, go-json); it must escape HTML itself |
| `DisableJSONHTMLEscape` | `false` | stop escaping `<`, `>`, `&` in `c.JSON` |
| `RedirectTrailingSlash` | `false` | redirect `/foo/` ↔ `/foo` when only the other exists |
| `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout` | 5s, 30s, 30s, 120s | server timeouts |
| `ShutdownTimeout` | 10s | graceful shutdown window for `Run` |

### Background work

A `*kite.Ctx` is reused after the handler returns, so never hand it to a goroutine. Call `c.Copy()` inside the handler instead:

```go
app.POST("/jobs", func(c *kite.Ctx) error {
	cp := c.Copy() // params, store and request context survive the request
	go process(cp) // cp cannot write a response or read the body
	return c.NoContent(http.StatusAccepted)
})
```

### Working with `net/http`

```go
app.GET("/metrics", kite.WrapHandler(promhttp.Handler())) // mount any http.Handler

http.ListenAndServe(":8080", app) // App is an http.Handler

srv := app.Server(":443") // *http.Server with safe timeouts
srv.ListenAndServeTLS("cert.pem", "key.pem")
```

### Testing

```go
func TestHealth(t *testing.T) {
	app := newApp() // your function that registers routes
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest("GET", "/health", nil))
	if w.Code != 200 || w.Body.String() != "ok" {
		t.Fatal(w.Code, w.Body)
	}
}
```

### Secure defaults

- `c.JSON` escapes `<`, `>` and `&`.
- `Bind` rejects non-JSON content types (415) and trailing data (400).
- `Static` never lists directories or serves dotfiles (`.env`, `.git`).
- `RequestID` only trusts client IDs of 1–64 `[A-Za-z0-9_-]` characters.
- Trailing-slash redirects never point to `//host`.
- `CORS` panics on `AllowOrigins: ["*"]` combined with `AllowCredentials: true`.

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
- `app.Use` must be called before registering routes (calling it afterwards panics so the mistake isn't silent). Global middleware also runs for 404/405 and trailing-slash redirects; group and route middleware only run on matched routes.
- Conflicting routes such as `/a/:id` and `/a/:name` panic at startup, not at request time.
- Current status is prototype: all unit tests and race tests pass, but it has not yet been proven in production.

## License

[MIT](LICENSE) © 2026 TF Soft Co., Ltd.
