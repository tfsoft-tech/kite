package kite

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func do(a *App, method, path, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	return w
}

func echoRoute(c *Ctx) error {
	var b strings.Builder
	b.WriteString(c.Route())
	for _, p := range c.Params() {
		b.WriteString("|" + p.Key + "=" + p.Value)
	}
	return c.String(b.String())
}

func TestRouting(t *testing.T) {
	a := New()
	routes := []string{
		"/", "/users", "/users/new", "/users/:id", "/users/:id/posts",
		"/users/:id/posts/:pid", "/static/*filepath", "/search", "/support",
		"/src/*path", "/u/:name/x", "/u/me/y", "/a/b/c", "/a/:x/d",
	}
	for _, r := range routes {
		a.GET(r, echoRoute)
	}
	cases := map[string]string{
		"/":                      "/",
		"/users":                 "/users",
		"/users/new":             "/users/new",
		"/users/42":              "/users/:id|id=42",
		"/users/42/posts":        "/users/:id/posts|id=42",
		"/users/42/posts/7":      "/users/:id/posts/:pid|id=42|pid=7",
		"/static/css/app.css":    "/static/*filepath|filepath=css/app.css",
		"/static/":               "/static/*filepath|filepath=",
		"/search":                "/search",
		"/support":               "/support",
		"/src/a/b/c.go":          "/src/*path|path=a/b/c.go",
		"/u/me/x":                "/u/:name/x|name=me", // backtrack from static "me" to param
		"/u/me/y":                "/u/me/y",
		"/a/b/c":                 "/a/b/c",
		"/a/b/d":                 "/a/:x/d|x=b", // backtrack
		"/users/%E0%B8%81/posts": "/users/:id/posts|id=ก",
	}
	for path, want := range cases {
		w := do(a, "GET", path, "")
		if w.Code != 200 || w.Body.String() != want {
			t.Errorf("%s: got %d %q want %q", path, w.Code, w.Body.String(), want)
		}
	}
	for _, p := range []string{"/nope", "/users/42/x", "/sea", "/u/me"} {
		if w := do(a, "GET", p, ""); w.Code != 404 {
			t.Errorf("%s: want 404 got %d", p, w.Code)
		}
	}
}

func TestMethodNotAllowedAndHead(t *testing.T) {
	a := New()
	a.GET("/x", func(c *Ctx) error { return c.String("ok") })
	a.PUT("/x", func(c *Ctx) error { return c.String("ok") })
	w := do(a, "POST", "/x", "")
	if w.Code != 405 || w.Header().Get("Allow") != "GET, PUT" {
		t.Fatalf("405: %d %q", w.Code, w.Header().Get("Allow"))
	}
	if w := do(a, "HEAD", "/x", ""); w.Code != 200 {
		t.Fatalf("HEAD fallback: %d", w.Code)
	}
}

func TestTrailingSlash(t *testing.T) {
	a := New(Config{RedirectTrailingSlash: true})
	a.GET("/x", func(c *Ctx) error { return nil })
	w := do(a, "GET", "/x/?a=1", "")
	if w.Code != 301 || w.Header().Get("Location") != "/x?a=1" {
		t.Fatalf("redirect: %d %q", w.Code, w.Header().Get("Location"))
	}
}

func TestMiddlewareOrderAndGroups(t *testing.T) {
	a := New()
	var trace []string
	mk := func(name string) Middleware {
		return func(next Handler) Handler {
			return func(c *Ctx) error { trace = append(trace, name); return next(c) }
		}
	}
	a.Use(mk("global"))
	api := a.Group("/api", mk("api"))
	v1 := api.Group("/v1", mk("v1"))
	v1.GET("/ping", func(c *Ctx) error { trace = append(trace, "h"); return c.String("pong") }, mk("route"))
	w := do(a, "GET", "/api/v1/ping", "")
	if w.Body.String() != "pong" || strings.Join(trace, ",") != "global,api,v1,route,h" {
		t.Fatalf("got %q trace %v", w.Body.String(), trace)
	}
}

func TestErrorsAndRecover(t *testing.T) {
	a := New()
	a.Use(Recover())
	a.GET("/boom", func(c *Ctx) error { panic("x") })
	a.GET("/teapot", func(c *Ctx) error { return NewError(418, "short and stout") })
	a.GET("/plain", func(c *Ctx) error { return errors.New("secret db detail") })
	if w := do(a, "GET", "/boom", ""); w.Code != 500 {
		t.Fatalf("panic: %d", w.Code)
	}
	if w := do(a, "GET", "/teapot", ""); w.Code != 418 || !strings.Contains(w.Body.String(), "short and stout") {
		t.Fatalf("teapot: %d %s", w.Code, w.Body)
	}
	w := do(a, "GET", "/plain", "")
	if w.Code != 500 || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("plain error leaked: %d %s", w.Code, w.Body)
	}
}

func TestJSONBindAndLimit(t *testing.T) {
	a := New(Config{BodyLimit: 64})
	type In struct {
		Name string `json:"name"`
	}
	a.POST("/echo", func(c *Ctx) error {
		var in In
		if err := c.Bind(&in); err != nil {
			return err
		}
		return c.Status(201).JSON(map[string]string{"hello": in.Name})
	})
	w := do(a, "POST", "/echo", `{"name":"สวัสดี"}`)
	if w.Code != 201 || strings.TrimSpace(w.Body.String()) != `{"hello":"สวัสดี"}` {
		t.Fatalf("bind: %d %s", w.Code, w.Body)
	}
	if w := do(a, "POST", "/echo", `{"bad":1}`); w.Code != 400 {
		t.Fatalf("unknown field: %d", w.Code)
	}
	if w := do(a, "POST", "/echo", `{"name":"`+strings.Repeat("a", 200)+`"}`); w.Code != 413 {
		t.Fatalf("limit: %d", w.Code)
	}
}

func TestQueryStoreCORS(t *testing.T) {
	a := New()
	a.Use(CORS(CORSConfig{AllowOrigins: []string{"https://ok.com"}}))
	a.GET("/q", func(c *Ctx) error {
		c.Set("k", "v")
		return c.String(c.Query("a") + c.Get("k").(string))
	})
	a.Handle("OPTIONS", "/q", func(c *Ctx) error { return nil })
	r := httptest.NewRequest("GET", "/q?a=1", nil)
	r.Header.Set("Origin", "https://ok.com")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Body.String() != "1v" || w.Header().Get("Access-Control-Allow-Origin") != "https://ok.com" {
		t.Fatalf("got %q %v", w.Body, w.Header())
	}
	r = httptest.NewRequest("OPTIONS", "/q", nil)
	r.Header.Set("Origin", "https://ok.com")
	r.Header.Set("Access-Control-Request-Method", "GET")
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatalf("preflight %d", w.Code)
	}
}

func TestDuplicateRoutePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	a := New()
	a.GET("/a/:id", echoRoute)
	a.GET("/a/:name", echoRoute)
}

// Zero allocations on the hot path (routing + params + text response).
type nopWriter struct{ h http.Header }

func (w *nopWriter) Header() http.Header               { return w.h }
func (w *nopWriter) Write(b []byte) (int, error)       { return len(b), nil }
func (w *nopWriter) WriteHeader(int)                   {}
func (w *nopWriter) WriteString(s string) (int, error) { return len(s), nil }

func TestZeroAlloc(t *testing.T) {
	a := New()
	a.GET("/users/:id/posts/:pid", func(c *Ctx) error { return c.String(c.Param("pid")) })
	r := httptest.NewRequest("GET", "/users/42/posts/7", nil)
	w := &nopWriter{h: http.Header{}}
	allocs := testing.AllocsPerRun(1000, func() { a.ServeHTTP(w, r) })
	if allocs != 0 {
		t.Fatalf("expected 0 allocs, got %v", allocs)
	}
}
