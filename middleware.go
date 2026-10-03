package kite

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
)

// Recover turns panics into 500 responses (http.ErrAbortHandler is re-raised).
func Recover() Middleware {
	return func(next Handler) Handler {
		return func(c *Ctx) (err error) {
			defer func() {
				if r := recover(); r != nil {
					if r == http.ErrAbortHandler {
						panic(r)
					}
					slog.Error("kite: panic", "err", r, "path", c.Path(), "stack", string(debug.Stack()))
					err = ErrInternal
				}
			}()
			return next(c)
		}
	}
}

// Logger logs one structured line per request via log/slog.
func Logger() Middleware {
	return func(next Handler) Handler {
		return func(c *Ctx) error {
			start := time.Now()
			err := next(c)
			if err != nil {
				c.app.cfg.ErrorHandler(c, err)
				err = nil
			}
			slog.Info("http",
				"method", c.Method(),
				"route", c.Route(),
				"path", c.Path(),
				"status", c.Response.status,
				"bytes", c.Response.size,
				"dur", time.Since(start),
			)
			return err
		}
	}
}

// RequestID sets/propagates X-Request-ID and stores it under "request_id".
// A client-supplied ID is kept only if it is 1-64 characters of [A-Za-z0-9_-];
// anything else is replaced with a fresh random ID.
func RequestID() Middleware {
	return func(next Handler) Handler {
		return func(c *Ctx) error {
			id := c.Header("X-Request-ID")
			if !validRequestID(id) {
				var b [8]byte
				_, _ = rand.Read(b[:])
				id = hex.EncodeToString(b[:])
			}
			c.SetHeader("X-Request-ID", id)
			c.Set("request_id", id)
			return next(c)
		}
	}
}

func validRequestID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for i := 0; i < len(id); i++ {
		ch := id[i]
		if !('a' <= ch && ch <= 'z' || 'A' <= ch && ch <= 'Z' || '0' <= ch && ch <= '9' || ch == '-' || ch == '_') {
			return false
		}
	}
	return true
}

// CORSConfig configures CORS.
type CORSConfig struct {
	AllowOrigins     []string // "*" allowed, but not together with AllowCredentials
	AllowMethods     []string
	AllowHeaders     []string
	AllowCredentials bool
	MaxAge           time.Duration
}

// CORS handles simple and preflight requests. As global middleware (app.Use)
// it also answers preflights for paths with no OPTIONS route; as group or
// route middleware, register the OPTIONS routes yourself.
//
// It panics if AllowOrigins contains "*" while AllowCredentials is set: that
// would let any site make credentialed requests. List the trusted origins.
func CORS(cfg CORSConfig) Middleware {
	if len(cfg.AllowMethods) == 0 {
		cfg.AllowMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}
	}
	methods := strings.Join(cfg.AllowMethods, ", ")
	headers := strings.Join(cfg.AllowHeaders, ", ")
	maxAge := strconv.Itoa(int(cfg.MaxAge.Seconds()))
	any := false
	allowed := map[string]bool{}
	for _, o := range cfg.AllowOrigins {
		if o == "*" {
			any = true
		}
		allowed[o] = true
	}
	if any && cfg.AllowCredentials {
		panic(`kite: CORS AllowOrigins "*" cannot be combined with AllowCredentials; list explicit origins`)
	}
	return func(next Handler) Handler {
		return func(c *Ctx) error {
			origin := c.Header("Origin")
			if origin == "" || !(any || allowed[origin]) {
				return next(c)
			}
			h := c.Response.Header()
			if any {
				h.Set("Access-Control-Allow-Origin", "*")
			} else {
				h.Set("Access-Control-Allow-Origin", origin)
				h.Add("Vary", "Origin")
			}
			if cfg.AllowCredentials {
				h.Set("Access-Control-Allow-Credentials", "true")
			}
			if c.Method() == http.MethodOptions && c.Header("Access-Control-Request-Method") != "" {
				h.Set("Access-Control-Allow-Methods", methods)
				if headers != "" {
					h.Set("Access-Control-Allow-Headers", headers)
				} else if req := c.Header("Access-Control-Request-Headers"); req != "" {
					h.Set("Access-Control-Allow-Headers", req)
				}
				if cfg.MaxAge > 0 {
					h.Set("Access-Control-Max-Age", maxAge)
				}
				return c.NoContent(http.StatusNoContent)
			}
			return next(c)
		}
	}
}

// SecureConfig configures Secure. Empty fields get the defaults noted.
type SecureConfig struct {
	ContentTypeNosniff string // X-Content-Type-Options, default "nosniff"
	XFrameOptions      string // default "SAMEORIGIN" (or "DENY")
	ReferrerPolicy     string // default "strict-origin-when-cross-origin"
	// ContentSecurityPolicy is sent only when set.
	ContentSecurityPolicy string
	// HSTSMaxAge > 0 enables Strict-Transport-Security. Only enable it for
	// sites served exclusively over HTTPS.
	HSTSMaxAge            time.Duration
	HSTSIncludeSubdomains bool
	HSTSPreload           bool
}

// Secure sets baseline security response headers.
func Secure(cfg ...SecureConfig) Middleware {
	var c SecureConfig
	if len(cfg) > 0 {
		c = cfg[0]
	}
	def := func(s *string, v string) {
		if *s == "" {
			*s = v
		}
	}
	def(&c.ContentTypeNosniff, "nosniff")
	def(&c.XFrameOptions, "SAMEORIGIN")
	def(&c.ReferrerPolicy, "strict-origin-when-cross-origin")
	hsts := ""
	if c.HSTSMaxAge > 0 {
		hsts = "max-age=" + strconv.FormatInt(int64(c.HSTSMaxAge.Seconds()), 10)
		if c.HSTSIncludeSubdomains {
			hsts += "; includeSubDomains"
		}
		if c.HSTSPreload {
			hsts += "; preload"
		}
	}
	return func(next Handler) Handler {
		return func(ctx *Ctx) error {
			h := ctx.Response.Header()
			h.Set("X-Content-Type-Options", c.ContentTypeNosniff)
			h.Set("X-Frame-Options", c.XFrameOptions)
			h.Set("Referrer-Policy", c.ReferrerPolicy)
			if c.ContentSecurityPolicy != "" {
				h.Set("Content-Security-Policy", c.ContentSecurityPolicy)
			}
			if hsts != "" {
				h.Set("Strict-Transport-Security", hsts)
			}
			return next(ctx)
		}
	}
}

// Timeout cancels the request context after d. Handlers should respect
// c.Request.Context().
func Timeout(d time.Duration) Middleware {
	return func(next Handler) Handler {
		return func(c *Ctx) error {
			ctx, cancel := context.WithTimeout(c.Request.Context(), d)
			defer cancel()
			c.Request = c.Request.WithContext(ctx)
			err := next(c)
			if err == nil && ctx.Err() != nil && !c.rw.written {
				return NewError(http.StatusServiceUnavailable, fmt.Sprintf("timeout after %s", d))
			}
			return err
		}
	}
}
