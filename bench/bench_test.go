package bench

import (
	"io"
	"net/http"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-chi/chi/v5"
	"github.com/julienschmidt/httprouter"
	"github.com/labstack/echo/v4"
	"github.com/tfsoft-tech/kite"
)

// ---- mock writer (implements WriteString like net/http's response) --------
type mockW struct{ h http.Header }

func newW() *mockW                                 { return &mockW{h: http.Header{}} }
func (w *mockW) Header() http.Header               { return w.h }
func (w *mockW) Write(b []byte) (int, error)       { return len(b), nil }
func (w *mockW) WriteString(s string) (int, error) { return len(s), nil }
func (w *mockW) WriteHeader(int)                   {}

var paramRE = regexp.MustCompile(`:(\w+)`)

func braces(p string) string { return paramRE.ReplaceAllString(p, "{$1}") }

// ---- loaders ---------------------------------------------------------------
func loadKite(rs []route, h kite.Handler) http.Handler {
	a := kite.New()
	for _, r := range rs {
		a.Handle(r.method, r.path, h)
	}
	return a
}
func loadGin(rs []route, h gin.HandlerFunc) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	e := gin.New()
	for _, r := range rs {
		e.Handle(r.method, r.path, h)
	}
	return e
}
func loadEcho(rs []route, h echo.HandlerFunc) http.Handler {
	e := echo.New()
	for _, r := range rs {
		e.Add(r.method, strings.Replace(r.path, "*filepath", "*", 1), h)
	}
	return e
}
func loadChi(rs []route, h http.HandlerFunc) http.Handler {
	m := chi.NewRouter()
	for _, r := range rs {
		m.MethodFunc(r.method, strings.Replace(braces(r.path), "*filepath", "*", 1), h)
	}
	return m
}
func loadHR(rs []route, h httprouter.Handle) http.Handler {
	m := httprouter.New()
	for _, r := range rs {
		m.Handle(r.method, r.path, h)
	}
	return m
}
func loadStd(rs []route, h http.HandlerFunc) http.Handler {
	m := http.NewServeMux()
	for _, r := range rs {
		m.HandleFunc(r.method+" "+strings.Replace(braces(r.path), "*filepath", "{filepath...}", 1), h)
	}
	return m
}

// ---- empty handlers (pure routing cost) ------------------------------------
var (
	kiteNop = func(c *kite.Ctx) error { return nil }
	ginNop  = func(c *gin.Context) {}
	echoNop = func(c echo.Context) error { return nil }
	stdNop  = func(w http.ResponseWriter, r *http.Request) {}
	hrNop   = func(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {}
)

type fw struct {
	name string
	load func([]route) http.Handler
}

var nops = []fw{
	{"Kite", func(r []route) http.Handler { return loadKite(r, kiteNop) }},
	{"Gin", func(r []route) http.Handler { return loadGin(r, ginNop) }},
	{"Echo", func(r []route) http.Handler { return loadEcho(r, echoNop) }},
	{"HttpRouter", func(r []route) http.Handler { return loadHR(r, hrNop) }},
	{"Chi", func(r []route) http.Handler { return loadChi(r, stdNop) }},
	{"StdMux", func(r []route) http.Handler { return loadStd(r, stdNop) }},
}

func benchOne(b *testing.B, h http.Handler, method, path string) {
	r, _ := http.NewRequest(method, path, nil)
	w := newW()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.ServeHTTP(w, r)
	}
}

func BenchmarkStatic(b *testing.B) {
	for _, f := range nops {
		b.Run(f.name, func(b *testing.B) { benchOne(b, f.load(githubAPI), "GET", "/user/repos") })
	}
}

func BenchmarkParam1(b *testing.B) {
	for _, f := range nops {
		b.Run(f.name, func(b *testing.B) { benchOne(b, f.load(githubAPI), "GET", "/users/gopher") })
	}
}

func BenchmarkParam4(b *testing.B) {
	for _, f := range nops {
		b.Run(f.name, func(b *testing.B) {
			benchOne(b, f.load(githubAPI), "GET", "/legacy/issues/search/golang/go/open/router")
		})
	}
}

func BenchmarkGitHubAll(b *testing.B) {
	reqs := make([]*http.Request, len(githubAPI))
	for i, r := range githubAPI {
		p := paramRE.ReplaceAllString(r.path, "x$1")
		reqs[i], _ = http.NewRequest(r.method, p, nil)
	}
	for _, f := range nops {
		b.Run(f.name, func(b *testing.B) {
			h := f.load(githubAPI)
			w := newW()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for _, r := range reqs {
					h.ServeHTTP(w, r)
				}
			}
		})
	}
}

// ---- realistic: read a param and return JSON --------------------------------
type user struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func BenchmarkJSONParam(b *testing.B) {
	rs := []route{{"GET", "/users/:id"}}
	fws := []fw{
		{"Kite", func(r []route) http.Handler {
			return loadKite(r, func(c *kite.Ctx) error { return c.JSON(user{c.Param("id"), "gopher"}) })
		}},
		{"Gin", func(r []route) http.Handler {
			return loadGin(r, func(c *gin.Context) { c.JSON(200, user{c.Param("id"), "gopher"}) })
		}},
		{"Echo", func(r []route) http.Handler {
			return loadEcho(r, func(c echo.Context) error { return c.JSON(200, user{c.Param("id"), "gopher"}) })
		}},
		{"Chi", func(r []route) http.Handler {
			return loadChi(r, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				writeJSON(w, user{chi.URLParam(r, "id"), "gopher"})
			})
		}},
		{"StdMux", func(r []route) http.Handler {
			return loadStd(r, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				writeJSON(w, user{r.PathValue("id"), "gopher"})
			})
		}},
	}
	for _, f := range fws {
		b.Run(f.name, func(b *testing.B) { benchOne(b, f.load(rs), "GET", "/users/42") })
	}
}

// ---- memory footprint of the router itself ----------------------------------
func BenchmarkRouterMemory(b *testing.B) {
	for _, f := range nops {
		b.Run(f.name, func(b *testing.B) {
			var keep http.Handler
			var m0, m1 runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&m0)
			keep = f.load(githubAPI)
			runtime.GC()
			runtime.ReadMemStats(&m1)
			b.ReportMetric(float64(m1.HeapAlloc-m0.HeapAlloc)/1024, "KB-heap")
			runtime.KeepAlive(keep)
			for i := 0; i < b.N; i++ {
			}
		})
	}
}

var _ = io.Discard
