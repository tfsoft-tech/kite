package kite

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
)

// Content types written by the response helpers.
const (
	MIMEJSON  = "application/json; charset=utf-8"
	MIMEText  = "text/plain; charset=utf-8"
	MIMEHTML  = "text/html; charset=utf-8"
	MIMEBytes = "application/octet-stream"
)

// responseWriter tracks status and size. It lives inside the pooled Ctx, so
// wrapping costs zero allocations.
type responseWriter struct {
	http.ResponseWriter
	status  int
	size    int
	written bool
}

func (w *responseWriter) reset(rw http.ResponseWriter) {
	w.ResponseWriter, w.status, w.size, w.written = rw, http.StatusOK, 0, false
}

func (w *responseWriter) WriteHeader(code int) {
	if w.written {
		return
	}
	w.status, w.written = code, true
	w.ResponseWriter.WriteHeader(code)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	if !w.written {
		w.WriteHeader(w.status)
	}
	n, err := w.ResponseWriter.Write(b)
	w.size += n
	return n, err
}

func (w *responseWriter) WriteString(s string) (int, error) {
	if !w.written {
		w.WriteHeader(w.status)
	}
	n, err := io.WriteString(w.ResponseWriter, s)
	w.size += n
	return n, err
}

// Unwrap lets http.ResponseController reach Flush / Hijack / deadlines.
func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *responseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		if !w.written {
			w.WriteHeader(w.status)
		}
		f.Flush()
	}
}

func (w *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		w.written = true
		return h.Hijack()
	}
	return nil, nil, errors.New("kite: hijack not supported")
}

// Ctx is the per-request context. It is pooled — never keep a reference to
// it after the handler returns (copy values out instead).
type Ctx struct {
	Request  *http.Request
	Response *responseWriter

	rw     responseWriter
	params []Param
	pbuf   [8]Param
	query  url.Values
	store  []kv
	app    *App
	route  string
}

type kv struct {
	k string
	v any
}

func (c *Ctx) reset(w http.ResponseWriter, r *http.Request) {
	c.rw.reset(w)
	c.Response = &c.rw
	c.Request = r
	c.params = c.pbuf[:0]
	c.query = nil
	c.store = c.store[:0]
	c.route = ""
}

// release drops every reference held from the finished request so a pooled
// Ctx neither keeps that request's memory alive nor exposes its values.
func (c *Ctx) release() {
	c.Request = nil
	c.rw.ResponseWriter = nil
	c.query = nil
	clear(c.pbuf[:])
	if cap(c.params) > len(c.pbuf) { // grew past inline array; drop it
		c.params = nil
	} else {
		c.params = c.pbuf[:0]
	}
	clear(c.store)
	c.store = c.store[:0]
}

// detachedWriter backs a copied Ctx: the original response is gone, so
// writes fail instead of touching a recycled http.ResponseWriter.
type detachedWriter struct{ h http.Header }

var errDetached = errors.New("kite: response is not available on a copied Ctx")

func (w *detachedWriter) Header() http.Header       { return w.h }
func (w *detachedWriter) Write([]byte) (int, error) { return 0, errDetached }
func (w *detachedWriter) WriteHeader(int)           {}

// Copy returns a non-pooled snapshot of c that is safe to use from another
// goroutine after the handler returns. Call it inside the handler. Params,
// store and the request are cloned; the request context keeps its values but
// is not canceled when the original request ends. Writing a response from
// the copy returns an error.
func (c *Ctx) Copy() *Ctx {
	cp := &Ctx{
		app:    c.app,
		route:  c.route,
		params: append([]Param(nil), c.params...),
		store:  append([]kv(nil), c.store...),
	}
	if c.Request != nil {
		cp.Request = c.Request.Clone(context.WithoutCancel(c.Request.Context()))
	}
	cp.rw.reset(&detachedWriter{h: http.Header{}})
	cp.rw.status = c.rw.status
	cp.Response = &cp.rw
	return cp
}

// ---- request ---------------------------------------------------------------

// Param returns a path parameter (":id" or "*path").
func (c *Ctx) Param(name string) string {
	for i := range c.params {
		if c.params[i].Key == name {
			return c.params[i].Value
		}
	}
	return ""
}

// Params returns all captured path params. Valid only during the request.
func (c *Ctx) Params() []Param { return c.params }

// ParamInt parses a path param as int.
func (c *Ctx) ParamInt(name string) (int, error) {
	v, err := strconv.Atoi(c.Param(name))
	if err != nil {
		return 0, NewError(http.StatusBadRequest, "invalid param "+name)
	}
	return v, nil
}

// Query returns a query-string value (parsed lazily, once).
func (c *Ctx) Query(name string) string {
	if c.query == nil {
		c.query = c.Request.URL.Query()
	}
	return c.query.Get(name)
}

// Header returns a request header.
func (c *Ctx) Header(name string) string { return c.Request.Header.Get(name) }

// Method / Path shortcuts.
func (c *Ctx) Method() string { return c.Request.Method }
func (c *Ctx) Path() string   { return c.Request.URL.Path }

// Route returns the matched route pattern, e.g. "/users/:id".
func (c *Ctx) Route() string { return c.route }

// Bind decodes a JSON body into v, capped by Config.BodyLimit. The request
// must be sent as application/json (415 otherwise), which keeps cross-origin
// "simple" form/text requests from reaching JSON handlers, and the body must
// hold exactly one JSON value.
func (c *Ctx) Bind(v any) error {
	if mt, _, err := mime.ParseMediaType(c.Header("Content-Type")); err != nil || mt != "application/json" {
		return NewError(http.StatusUnsupportedMediaType, "content-type must be application/json")
	}
	body := http.MaxBytesReader(c.Response, c.Request.Body, c.app.cfg.BodyLimit)
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return bindError(err)
	}
	if _, err := dec.Token(); err != io.EOF {
		if err != nil {
			if he := bindError(err); he.Code == http.StatusRequestEntityTooLarge {
				return he
			}
		}
		return NewError(http.StatusBadRequest, "invalid JSON: unexpected data after top-level value")
	}
	return nil
}

func bindError(err error) *HTTPError {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return NewError(http.StatusRequestEntityTooLarge, "request body too large")
	}
	return NewError(http.StatusBadRequest, "invalid JSON: "+err.Error())
}

// Set / Get store request-scoped values without allocating a map.
func (c *Ctx) Set(key string, v any) {
	for i := range c.store {
		if c.store[i].k == key {
			c.store[i].v = v
			return
		}
	}
	c.store = append(c.store, kv{key, v})
}

func (c *Ctx) Get(key string) any {
	for i := range c.store {
		if c.store[i].k == key {
			return c.store[i].v
		}
	}
	return nil
}

// ---- response --------------------------------------------------------------

// Status sets the status code for the next write. Chainable.
func (c *Ctx) Status(code int) *Ctx { c.rw.status = code; return c }

// SetHeader sets a response header.
func (c *Ctx) SetHeader(k, v string) { c.rw.Header().Set(k, v) }

// String writes a text/plain body.
func (c *Ctx) String(s string) error {
	c.rw.Header().Set("Content-Type", MIMEText)
	_, err := c.rw.WriteString(s)
	return err
}

// HTML writes a text/html body.
func (c *Ctx) HTML(s string) error {
	c.rw.Header().Set("Content-Type", MIMEHTML)
	_, err := c.rw.WriteString(s)
	return err
}

// Bytes writes a raw body with the given content type ("" = octet-stream).
func (c *Ctx) Bytes(contentType string, b []byte) error {
	if contentType == "" {
		contentType = MIMEBytes
	}
	c.rw.Header().Set("Content-Type", contentType)
	_, err := c.rw.Write(b)
	return err
}

// NoContent writes only a status code.
func (c *Ctx) NoContent(code int) error {
	c.rw.WriteHeader(code)
	return nil
}

// Redirect sends a redirect.
func (c *Ctx) Redirect(code int, url string) error {
	http.Redirect(&c.rw, c.Request, url, code)
	return nil
}

type jsonBuf struct {
	buf bytes.Buffer
	enc *json.Encoder
}

var jsonPool = sync.Pool{New: func() any {
	j := &jsonBuf{}
	j.enc = json.NewEncoder(&j.buf)
	return j
}}

// JSON encodes v. Uses the pluggable Config.JSONMarshal if set, otherwise
// encoding/json with a pooled buffer + encoder. <, > and & are escaped unless
// Config.DisableJSONHTMLEscape is set.
func (c *Ctx) JSON(v any) error {
	c.rw.Header().Set("Content-Type", MIMEJSON)
	if m := c.app.cfg.JSONMarshal; m != nil {
		b, err := m(v)
		if err != nil {
			return err
		}
		_, err = c.rw.Write(b)
		return err
	}
	j := jsonPool.Get().(*jsonBuf)
	j.buf.Reset()
	j.enc.SetEscapeHTML(!c.app.cfg.DisableJSONHTMLEscape)
	err := j.enc.Encode(v)
	if err == nil {
		_, err = c.rw.Write(j.buf.Bytes())
	}
	if j.buf.Cap() <= 64<<10 { // don't keep huge buffers alive
		jsonPool.Put(j)
	}
	return err
}

// Stream copies r to the response.
func (c *Ctx) Stream(contentType string, r io.Reader) error {
	if contentType != "" {
		c.rw.Header().Set("Content-Type", contentType)
	}
	_, err := io.Copy(&c.rw, r)
	return err
}
