package kite

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func do(a *App, method, path, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
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
	a.GET("/users/:id/posts/:pid", func(c *Ctx) error {
		_, err := c.Response.WriteString(c.Param("pid"))
		return err
	})
	a.GET("/text/:id", func(c *Ctx) error { return c.String(c.Param("id")) })
	w := &nopWriter{h: http.Header{}}
	r := httptest.NewRequest("GET", "/users/42/posts/7", nil)
	if allocs := testing.AllocsPerRun(1000, func() { a.ServeHTTP(w, r) }); allocs != 0 {
		t.Fatalf("routing: expected 0 allocs, got %v", allocs)
	}
	// Setting Content-Type costs one fresh []string per response (shared
	// global slices would be a cross-request race).
	r = httptest.NewRequest("GET", "/text/7", nil)
	if allocs := testing.AllocsPerRun(1000, func() { a.ServeHTTP(w, r) }); allocs != 1 {
		t.Fatalf("String: expected 1 alloc, got %v", allocs)
	}
}

func TestCORSWildcardWithCredentialsPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	CORS(CORSConfig{AllowOrigins: []string{"*"}, AllowCredentials: true})
}

func TestCORSOnlyReflectsListedOrigins(t *testing.T) {
	a := New()
	a.Use(CORS(CORSConfig{AllowOrigins: []string{"https://ok.com"}, AllowCredentials: true}))
	a.GET("/q", func(c *Ctx) error { return c.String("x") })
	r := httptest.NewRequest("GET", "/q", nil)
	r.Header.Set("Origin", "https://evil.com")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Header().Get("Access-Control-Allow-Origin") != "" || w.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Fatalf("untrusted origin got CORS headers: %v", w.Header())
	}

	a = New()
	a.Use(CORS(CORSConfig{AllowOrigins: []string{"*"}}))
	a.GET("/q", func(c *Ctx) error { return c.String("x") })
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("wildcard: %v", w.Header())
	}
}

func TestJSONEscapesHTML(t *testing.T) {
	h := func(c *Ctx) error { return c.JSON(map[string]string{"x": "<script>"}) }
	a := New()
	a.GET("/j", h)
	if got := strings.TrimSpace(do(a, "GET", "/j", "").Body.String()); got != `{"x":"\u003cscript\u003e"}` {
		t.Fatalf("default: %s", got)
	}
	a = New(Config{DisableJSONHTMLEscape: true})
	a.GET("/j", h)
	if got := strings.TrimSpace(do(a, "GET", "/j", "").Body.String()); got != `{"x":"<script>"}` {
		t.Fatalf("disabled: %s", got)
	}
}

func TestBindContentTypeAndTrailingData(t *testing.T) {
	a := New()
	a.POST("/b", func(c *Ctx) error {
		var in struct {
			Name string `json:"name"`
		}
		if err := c.Bind(&in); err != nil {
			return err
		}
		return c.String(in.Name)
	})
	post := func(ct, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/b", strings.NewReader(body))
		if ct != "" {
			r.Header.Set("Content-Type", ct)
		}
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	for _, ct := range []string{"text/plain", "", "application/x-www-form-urlencoded", "application/jsonx"} {
		if w := post(ct, `{"name":"a"}`); w.Code != 415 {
			t.Errorf("content-type %q: want 415 got %d", ct, w.Code)
		}
	}
	if w := post("Application/JSON; charset=utf-8", `{"name":"a"}`+"\n"); w.Code != 200 || w.Body.String() != "a" {
		t.Errorf("valid: %d %s", w.Code, w.Body)
	}
	for _, body := range []string{`{"name":"a"}garbage`, `{"name":"a"}{"name":"b"}`, `{"name":"a"} 1`} {
		if w := post("application/json", body); w.Code != 400 {
			t.Errorf("%q: want 400 got %d", body, w.Code)
		}
	}
}

func TestCtxReleaseClearsReferences(t *testing.T) {
	a := New()
	c := a.pool.Get().(*Ctx)
	c.reset(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	c.params = append(c.params, Param{Key: "id", Value: "secret"})
	c.Set("user", &struct{}{})
	c.release()
	if c.pbuf[0] != (Param{}) {
		t.Fatalf("param retained: %+v", c.pbuf[0])
	}
	if s := c.store[:cap(c.store)]; len(s) == 0 || s[0] != (kv{}) {
		t.Fatalf("store retained: %+v", s)
	}
	if c.Request != nil || c.rw.ResponseWriter != nil {
		t.Fatal("request/response retained")
	}
}

func TestConcurrentHeaders(t *testing.T) {
	a := New()
	a.GET("/s", func(c *Ctx) error {
		if err := c.String("x"); err != nil {
			return err
		}
		c.Response.Header()["Content-Type"][0] = "mutated"
		return nil
	})
	a.GET("/j", func(c *Ctx) error {
		err := c.JSON(1)
		c.Response.Header()["Content-Type"][0] = "mutated"
		return err
	})
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, p := range []string{"/s", "/j"} {
				do(a, "GET", p, "")
			}
		}()
	}
	wg.Wait()
	b := New()
	b.GET("/s", func(c *Ctx) error { return c.String("x") })
	if ct := do(b, "GET", "/s", "").Header().Get("Content-Type"); ct != MIMEText {
		t.Fatalf("content type corrupted: %q", ct)
	}
}

func TestTrailingSlashNoOpenRedirect(t *testing.T) {
	a := New(Config{RedirectTrailingSlash: true})
	a.GET("/:a", func(c *Ctx) error { return nil })
	a.GET("/:a/:b/:c", func(c *Ctx) error { return nil })
	for _, p := range []string{"///attacker.com/", "//attacker.com/", "/\\attacker.com/", "///attacker.com/x/y/"} {
		w := do(a, "GET", p, "")
		if loc := w.Header().Get("Location"); w.Code/100 == 3 || strings.HasPrefix(loc, "//") {
			t.Errorf("%s: redirected %d to %q", p, w.Code, loc)
		}
	}
}

func TestStaticNoDirectoryListing(t *testing.T) {
	dir := t.TempDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(dir, "list"), 0o755))
	must(os.MkdirAll(filepath.Join(dir, "site"), 0o755))
	must(os.WriteFile(filepath.Join(dir, "list", "secret.txt"), []byte("s"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "site", "index.html"), []byte("home"), 0o644))
	a := New()
	a.Static("/assets", dir)
	for _, p := range []string{"/assets/", "/assets/list/"} {
		if w := do(a, "GET", p, ""); w.Code != 404 || strings.Contains(w.Body.String(), "secret") {
			t.Errorf("%s: %d %s", p, w.Code, w.Body)
		}
	}
	if w := do(a, "GET", "/assets/site/", ""); w.Code != 200 || w.Body.String() != "home" {
		t.Errorf("index: %d %s", w.Code, w.Body)
	}
	if w := do(a, "GET", "/assets/list/secret.txt", ""); w.Code != 200 {
		t.Errorf("file: %d", w.Code)
	}
}

func TestSecureHeaders(t *testing.T) {
	a := New()
	a.Use(Secure())
	a.GET("/", func(c *Ctx) error { return c.String("x") })
	h := do(a, "GET", "/", "").Header()
	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "SAMEORIGIN",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	}
	for k, v := range want {
		if h.Get(k) != v {
			t.Errorf("%s = %q want %q", k, h.Get(k), v)
		}
	}
	if h.Get("Strict-Transport-Security") != "" {
		t.Error("HSTS should be off by default")
	}

	a = New()
	a.Use(Secure(SecureConfig{XFrameOptions: "DENY", HSTSMaxAge: 365 * 24 * time.Hour, HSTSIncludeSubdomains: true}))
	a.GET("/", func(c *Ctx) error { return c.String("x") })
	h = do(a, "GET", "/", "").Header()
	if h.Get("X-Frame-Options") != "DENY" || h.Get("Strict-Transport-Security") != "max-age=31536000; includeSubDomains" {
		t.Fatalf("custom: %v", h)
	}
}

func TestRequestIDValidation(t *testing.T) {
	a := New()
	a.Use(RequestID())
	a.GET("/", func(c *Ctx) error { return c.String(c.Get("request_id").(string)) })
	get := func(id string) string {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("X-Request-ID", id)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		if w.Body.String() != w.Header().Get("X-Request-ID") {
			t.Fatalf("store/header mismatch")
		}
		return w.Body.String()
	}
	if got := get("abc-123_XYZ"); got != "abc-123_XYZ" {
		t.Errorf("valid id replaced: %q", got)
	}
	for _, bad := range []string{strings.Repeat("a", 65), "a b", "x\"y", "<script>", "id;drop", "ก"} {
		got := get(bad)
		if got == bad || !validRequestID(got) || len(got) != 16 {
			t.Errorf("%q: got %q", bad, got)
		}
	}
}

func TestCopyOutlivesRequest(t *testing.T) {
	a := New()
	a.Use(Timeout(time.Second))
	done := make(chan string, 1)
	a.GET("/u/:id", func(c *Ctx) error {
		c.Set("user", "alice")
		cp := c.Copy()
		go func() {
			time.Sleep(20 * time.Millisecond) // after the original Ctx was recycled
			if cp.Request.Context().Err() != nil {
				done <- "context canceled"
				return
			}
			if err := cp.String("late"); err == nil {
				done <- "write on copy should fail"
				return
			}
			done <- cp.Param("id") + "|" + cp.Get("user").(string) + "|" + cp.Route() + "|" + cp.Query("q")
		}()
		return c.String("ok")
	})
	a.GET("/other/:x", func(c *Ctx) error { c.Set("user", "mallory"); return c.String("x") })
	do(a, "GET", "/u/42?q=1", "")
	for i := 0; i < 20; i++ { // reuse pooled Ctxs while the copy is in use
		do(a, "GET", "/other/99?q=2", "")
	}
	if got := <-done; got != "42|alice|/u/:id|1" {
		t.Fatalf("copy: %s", got)
	}
}

func TestStaticHidesDotfiles(t *testing.T) {
	dir := t.TempDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
	must(os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	must(os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=1"), 0o644))
	must(os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("SECRET=2"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "sub", ".htpasswd"), []byte("SECRET=3"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "ok.txt"), []byte("ok"), 0o644))
	a := New()
	a.Static("/a", dir)
	for _, p := range []string{"/a/.env", "/a/.git/config", "/a/sub/.htpasswd", "/a/.git/", "/a/%2eenv", "/a/sub/../.env"} {
		if w := do(a, "GET", p, ""); w.Code != 404 || strings.Contains(w.Body.String(), "SECRET") {
			t.Errorf("%s: %d %s", p, w.Code, w.Body)
		}
	}
	if w := do(a, "GET", "/a/ok.txt", ""); w.Code != 200 || w.Body.String() != "ok" {
		t.Errorf("ok.txt: %d %s", w.Code, w.Body)
	}
}

func TestGlobalMiddlewareOnUnmatched(t *testing.T) {
	a := New(Config{RedirectTrailingSlash: true})
	a.Use(Secure())
	api := a.Group("/api", func(next Handler) Handler {
		return func(c *Ctx) error { c.SetHeader("X-Group", "1"); return next(c) }
	})
	api.GET("/x", func(c *Ctx) error { return c.String("x") })
	cases := map[string]struct {
		method, path string
		code         int
	}{
		"404":      {"GET", "/nope", 404},
		"405":      {"POST", "/api/x", 405},
		"redirect": {"GET", "/api/x/", 301},
		"hit":      {"GET", "/api/x", 200},
	}
	for name, tc := range cases {
		w := do(a, tc.method, tc.path, "")
		if w.Code != tc.code || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("X-Frame-Options") != "SAMEORIGIN" {
			t.Errorf("%s: %d %v", name, w.Code, w.Header())
		}
		if got := w.Header().Get("X-Group"); (name == "hit") != (got == "1") {
			t.Errorf("%s: group middleware ran=%q", name, got)
		}
	}
	if w := do(a, "POST", "/api/x", ""); w.Header().Get("Allow") != "GET" {
		t.Errorf("Allow lost: %v", w.Header())
	}

	// Use before any route still applies to misses; app without middleware unchanged.
	b := New()
	b.Use(Secure())
	if w := do(b, "GET", "/", ""); w.Code != 404 || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("no routes: %d %v", w.Code, w.Header())
	}
	if w := do(New(), "GET", "/", ""); w.Code != 404 || w.Header().Get("X-Content-Type-Options") != "" {
		t.Errorf("plain app: %d %v", w.Code, w.Header())
	}
}

func TestCORSPreflightWithoutOptionsRoute(t *testing.T) {
	a := New()
	a.Use(CORS(CORSConfig{AllowOrigins: []string{"https://ok.com"}}))
	a.POST("/p", func(c *Ctx) error { return nil })
	r := httptest.NewRequest("OPTIONS", "/p", nil)
	r.Header.Set("Origin", "https://ok.com")
	r.Header.Set("Access-Control-Request-Method", "POST")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 204 || w.Header().Get("Access-Control-Allow-Origin") != "https://ok.com" {
		t.Fatalf("preflight: %d %v", w.Code, w.Header())
	}
}
