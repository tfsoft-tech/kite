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
func RequestID() Middleware {
	return func(next Handler) Handler {
		return func(c *Ctx) error {
			id := c.Header("X-Request-ID")
			if id == "" {
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

// CORSConfig configures CORS.
type CORSConfig struct {
	AllowOrigins     []string // "*" allowed
	AllowMethods     []string
	AllowHeaders     []string
	AllowCredentials bool
	MaxAge           time.Duration
}

// CORS handles simple and preflight requests. Register OPTIONS routes or use
// it as global middleware together with app.Handle("OPTIONS", "/*path", ...).
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
	return func(next Handler) Handler {
		return func(c *Ctx) error {
			origin := c.Header("Origin")
			if origin == "" || !(any || allowed[origin]) {
				return next(c)
			}
			h := c.Response.Header()
			if any && !cfg.AllowCredentials {
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
