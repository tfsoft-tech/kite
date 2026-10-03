package kite

import (
	"errors"
	"log/slog"
	"net/http"
)

// HTTPError is an error carrying an HTTP status code.
type HTTPError struct {
	Code    int    `json:"-"`
	Message string `json:"error"`
}

func (e *HTTPError) Error() string { return e.Message }

// NewError creates an HTTPError.
func NewError(code int, msg string) *HTTPError { return &HTTPError{Code: code, Message: msg} }

var (
	ErrNotFound         = NewError(http.StatusNotFound, "not found")
	ErrMethodNotAllowed = NewError(http.StatusMethodNotAllowed, "method not allowed")
	ErrUnauthorized     = NewError(http.StatusUnauthorized, "unauthorized")
	ErrForbidden        = NewError(http.StatusForbidden, "forbidden")
	ErrInternal         = NewError(http.StatusInternalServerError, "internal server error")
)

// DefaultErrorHandler writes {"error": "..."} with the right status. Non-HTTP
// errors are logged and hidden behind a generic 500.
func DefaultErrorHandler(c *Ctx, err error) {
	if c.rw.written {
		return // headers already sent; nothing safe to do
	}
	var he *HTTPError
	if !errors.As(err, &he) {
		slog.Error("kite: handler error", "method", c.Method(), "path", c.Path(), "err", err)
		he = ErrInternal
	}
	c.Status(he.Code)
	_ = c.JSON(he)
}
