// Package kite is a small, fast, low-allocation HTTP framework built on
// net/http.
//
// Design choices that keep it fast and light:
//   - Compressed radix router with static > param > wildcard priority and
//     zero allocations per lookup (params live in a fixed array in Ctx).
//   - Ctx and its response wrapper are pooled; a request through the router
//     costs 0 allocs in the hot path.
//   - Middleware is composed once at registration time, so there is no
//     per-request chain walking.
//   - Handlers return error; one central ErrorHandler formats failures.
//   - Pure standard library: works with any http.Server, HTTP/2, TLS, httptest.
package kite

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Handler handles a request.
type Handler func(c *Ctx) error

// Middleware wraps a Handler.
type Middleware func(next Handler) Handler

// Config tunes the App. Zero values get sensible defaults.
type Config struct {
	// BodyLimit caps request bodies read via Ctx.Bind (default 4 MiB).
	BodyLimit int64
	// ErrorHandler formats errors returned by handlers.
	ErrorHandler func(c *Ctx, err error)
	// NotFound / MethodNotAllowed override default responses.
	NotFound Handler
	// JSONMarshal plugs in a faster encoder (e.g. sonic, go-json).
	JSONMarshal func(v any) ([]byte, error)
	// DisableJSONHTMLEscape stops Ctx.JSON from escaping <, > and & as
	// \u003c etc. Only set it when output is never embedded in HTML.
	// It does not apply to a custom JSONMarshal.
	DisableJSONHTMLEscape bool
	// RedirectTrailingSlash redirects /foo/ <-> /foo when only the other exists.
	RedirectTrailingSlash bool
	// Server timeouts (defaults: read header 5s, read 30s, write 30s, idle 120s).
	ReadHeaderTimeout, ReadTimeout, WriteTimeout, IdleTimeout time.Duration
	// ShutdownTimeout for graceful stop (default 10s).
	ShutdownTimeout time.Duration
}

// App is the framework entry point. It implements http.Handler.
type App struct {
	cfg    Config
	router router
	mw     []Middleware
	pool   sync.Pool
	frozen bool
}

// New creates an App.
func New(cfg ...Config) *App {
	a := &App{}
	if len(cfg) > 0 {
		a.cfg = cfg[0]
	}
	c := &a.cfg
	if c.BodyLimit == 0 {
		c.BodyLimit = 4 << 20
	}
	if c.ErrorHandler == nil {
		c.ErrorHandler = DefaultErrorHandler
	}
	setDur(&c.ReadHeaderTimeout, 5*time.Second)
	setDur(&c.ReadTimeout, 30*time.Second)
	setDur(&c.WriteTimeout, 30*time.Second)
	setDur(&c.IdleTimeout, 120*time.Second)
	setDur(&c.ShutdownTimeout, 10*time.Second)
	a.pool.New = func() any { return &Ctx{app: a} }
	return a
}

func setDur(d *time.Duration, def time.Duration) {
	if *d == 0 {
		*d = def
	}
}

// Use adds global middleware. Must be called before routes are registered
// (middleware is baked into each route at registration time).
func (a *App) Use(m ...Middleware) {
	if a.frozen {
		panic("kite: Use must be called before registering routes")
	}
	a.mw = append(a.mw, m...)
}

func (a *App) handle(method, path string, h Handler, mw []Middleware) {
	a.frozen = true
	all := make([]Middleware, 0, len(a.mw)+len(mw))
	all = append(all, a.mw...)
	all = append(all, mw...)
	a.router.add(method, path, chain(h, all))
}

func chain(h Handler, mw []Middleware) Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}

func (a *App) Handle(method, path string, h Handler, mw ...Middleware) {
	a.handle(method, path, h, mw)
}
func (a *App) GET(p string, h Handler, mw ...Middleware)    { a.handle("GET", p, h, mw) }
func (a *App) POST(p string, h Handler, mw ...Middleware)   { a.handle("POST", p, h, mw) }
func (a *App) PUT(p string, h Handler, mw ...Middleware)    { a.handle("PUT", p, h, mw) }
func (a *App) PATCH(p string, h Handler, mw ...Middleware)  { a.handle("PATCH", p, h, mw) }
func (a *App) DELETE(p string, h Handler, mw ...Middleware) { a.handle("DELETE", p, h, mw) }

// Static serves files from dir under prefix (e.g. "/assets"). Directories
// are served only through their index.html; there is no directory listing.
func (a *App) Static(prefix, dir string) {
	fs := http.StripPrefix(prefix, http.FileServer(noListFS{http.Dir(dir)}))
	a.GET(strings.TrimSuffix(prefix, "/")+"/*filepath", WrapHandler(fs))
}

// noListFS hides directories that have no index.html, so http.FileServer
// answers 404 instead of generating a listing.
type noListFS struct{ fs http.FileSystem }

func (n noListFS) Open(name string) (http.File, error) {
	f, err := n.fs.Open(name)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if st.IsDir() {
		idx, err := n.fs.Open(strings.TrimSuffix(name, "/") + "/index.html")
		if err != nil {
			f.Close()
			return nil, os.ErrNotExist
		}
		idx.Close()
	}
	return f, nil
}

// WrapHandler adapts any http.Handler (e.g. pprof, promhttp) to kite.
func WrapHandler(h http.Handler) Handler {
	return func(c *Ctx) error { h.ServeHTTP(c.Response, c.Request); return nil }
}

// Group is a route prefix with its own middleware.
type Group struct {
	app    *App
	prefix string
	mw     []Middleware
}

func (a *App) Group(prefix string, mw ...Middleware) *Group {
	return &Group{app: a, prefix: prefix, mw: mw}
}

func (g *Group) Group(prefix string, mw ...Middleware) *Group {
	all := append(append([]Middleware{}, g.mw...), mw...)
	return &Group{app: g.app, prefix: g.prefix + prefix, mw: all}
}

func (g *Group) Use(mw ...Middleware) { g.mw = append(g.mw, mw...) }

func (g *Group) Handle(method, p string, h Handler, mw ...Middleware) {
	all := append(append([]Middleware{}, g.mw...), mw...)
	g.app.handle(method, g.prefix+p, h, all)
}
func (g *Group) GET(p string, h Handler, mw ...Middleware)    { g.Handle("GET", p, h, mw...) }
func (g *Group) POST(p string, h Handler, mw ...Middleware)   { g.Handle("POST", p, h, mw...) }
func (g *Group) PUT(p string, h Handler, mw ...Middleware)    { g.Handle("PUT", p, h, mw...) }
func (g *Group) PATCH(p string, h Handler, mw ...Middleware)  { g.Handle("PATCH", p, h, mw...) }
func (g *Group) DELETE(p string, h Handler, mw ...Middleware) { g.Handle("DELETE", p, h, mw...) }

// ServeHTTP implements http.Handler — the hot path.
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c := a.pool.Get().(*Ctx)
	c.reset(w, r)

	path := r.URL.Path
	var n *node
	if t := a.router.tree(r.Method, false); t != nil {
		n = t.find(path, &c.params)
	}
	// HEAD falls back to GET.
	if n == nil && r.Method == http.MethodHead {
		if t := a.router.trees[mGET]; t != nil {
			c.params = c.pbuf[:0]
			n = t.find(path, &c.params)
		}
	}

	var err error
	if n != nil {
		c.route = n.pattern
		err = n.handler(c)
	} else {
		err = a.miss(c, path)
	}
	if err != nil {
		a.cfg.ErrorHandler(c, err)
	}

	c.release()
	a.pool.Put(c)
}

func (a *App) miss(c *Ctx, path string) error {
	if a.cfg.RedirectTrailingSlash && path != "/" {
		alt := path + "/"
		if strings.HasSuffix(path, "/") {
			alt = path[:len(path)-1]
		}
		// "//host" or "/\host" in Location is read by browsers as another
		// origin (open redirect), so never redirect to such a path.
		if strings.HasPrefix(alt, "//") || strings.HasPrefix(alt, "/\\") {
			alt = ""
		}
		if t := a.router.tree(c.Request.Method, false); t != nil && alt != "" {
			c.params = c.pbuf[:0]
			if t.find(alt, &c.params) != nil {
				code := http.StatusMovedPermanently
				if c.Request.Method != http.MethodGet {
					code = http.StatusPermanentRedirect
				}
				u := *c.Request.URL
				u.Path = alt
				return c.Redirect(code, u.String())
			}
		}
	}
	if allow := a.router.allowed(c.Request.Method, path); allow != "" {
		c.SetHeader("Allow", allow)
		return ErrMethodNotAllowed
	}
	if a.cfg.NotFound != nil {
		return a.cfg.NotFound(c)
	}
	return ErrNotFound
}

// Run starts the server and shuts down gracefully on SIGINT/SIGTERM.
func (a *App) Run(addr string) error {
	srv := a.Server(addr)
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-errc:
		return err
	case <-sig:
	}
	slog.Info("kite: shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), a.cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		return err
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Server returns a hardened *http.Server for custom setups (TLS, etc.).
func (a *App) Server(addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           a,
		ReadHeaderTimeout: a.cfg.ReadHeaderTimeout,
		ReadTimeout:       a.cfg.ReadTimeout,
		WriteTimeout:      a.cfg.WriteTimeout,
		IdleTimeout:       a.cfg.IdleTimeout,
		MaxHeaderBytes:    1 << 20,
	}
}
