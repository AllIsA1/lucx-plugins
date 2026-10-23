// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostlink

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/web/session"
)

// The two engines seams exist in.
const (
	EngineAPI = "api" // the panel's API, routes under /panel/api
	EngineSub = "sub" // the subscription server
)

// SeamMiddleware lets plugins take part in how the panel handles a request, through the plugin
// system service: before the panel's own handler (look at or change the request, or answer instead)
// and after it (change the answer). resolve gives the route pattern relative to the engine, or "" for
// a request that is no seam's business.
//
// A request nobody listens to costs one table lookup. When the service does not answer, is slow or
// answers wrongly, the request goes through as if there were no plugin. Calls the service itself
// makes for a plugin never pass through a seam, so a plugin cannot be asked about its own calls.
func SeamMiddleware(engine string, resolve func(*gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		route := resolve(c)
		if route == "" || FromHost(c) || seamStreaming(c.Request) {
			c.Next()
			return
		}
		link := Shared()
		pre, post := link.SeamWanted(engine, c.Request.Method, route)
		if !pre && !post {
			c.Next()
			return
		}
		body, ok := bodyForSeams(c.Request)
		if !ok {
			c.Next()
			return
		}
		req := SeamRequest{
			Engine: engine, Method: c.Request.Method, Route: route, Path: c.Request.URL.Path, Query: c.Request.URL.RawQuery,
			Headers: seamRequestHeaders(c.Request.Header), Body: body,
		}
		if len(c.Params) > 0 {
			req.Params = make(map[string]string, len(c.Params))
			for _, p := range c.Params {
				req.Params[p.Key] = p.Value
			}
		}
		if engine == EngineAPI {
			if u := session.GetLoginUser(c); u != nil {
				req.User = u.Username
			}
		}
		ctx := c.Request.Context()
		if pre {
			if res, err := link.SeamPre(ctx, req); err == nil {
				if res.Responded {
					writeSeamResponse(c, res.Response)
					c.Abort()
					return
				}
				req = applySeamRequest(c.Request, req, res.Request)
			}
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(req.Body))
		if !post {
			c.Next()
			return
		}
		runWithAfterSeams(ctx, c, link, req)
	}
}

// seamStreaming: WebSocket upgrades and event streams are never buffered.
func seamStreaming(r *http.Request) bool {
	return r.Header.Get("Upgrade") != "" || strings.Contains(r.Header.Get("Accept"), "text/event-stream")
}

// bodyForSeams reads the request body for the plugin system. A body too large for seams is left
// intact for the handler and the request goes without seams (false).
func bodyForSeams(r *http.Request) ([]byte, bool) {
	if r.Body == nil || r.Body == http.NoBody {
		return nil, true
	}
	if r.ContentLength > MaxSeamRequestBody {
		return nil, false
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, MaxSeamRequestBody+1))
	if err != nil || len(raw) > MaxSeamRequestBody {
		r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(raw), r.Body))
		return nil, false
	}
	return raw, true
}

var (
	panelOnlyRequestHeaders = map[string]bool{
		"authorization": true, "proxy-authorization": true, "cookie": true, "host": true, "content-length": true,
		"transfer-encoding": true, "connection": true, "keep-alive": true, "upgrade": true, "te": true, "trailer": true,
		"x-csrf-token": true, "x-forwarded-for": true, "x-forwarded-host": true, "x-forwarded-proto": true,
		"x-real-ip": true, "forwarded": true,
	}
	panelOnlyResponseHeaders = map[string]bool{
		"set-cookie": true, "content-length": true, "transfer-encoding": true, "connection": true, "keep-alive": true,
		"upgrade": true, "trailer": true, "te": true, "host": true,
		"content-security-policy": true, "content-security-policy-report-only": true, "x-frame-options": true,
		"strict-transport-security": true, "referrer-policy": true, "x-content-type-options": true,
		"permissions-policy": true, "clear-site-data": true, "alt-svc": true,
		"access-control-allow-origin": true, "access-control-allow-credentials": true, "access-control-allow-methods": true,
		"access-control-allow-headers": true, "access-control-expose-headers": true,
		"authorization": true, "www-authenticate": true, "proxy-authenticate": true, "proxy-authorization": true,
	}
	// managedResponseHeaders belong to the transport (compression, length): plugins never see or set them.
	managedResponseHeaders = map[string]bool{"content-encoding": true, "content-length": true, "transfer-encoding": true}
)

func denied(set map[string]bool, name string) bool {
	lower := strings.ToLower(name)
	return set[lower] || strings.HasPrefix(lower, "x-lucx-")
}

// seamRequestHeaders is what a plugin may see of the request: nothing that identifies the session.
func seamRequestHeaders(h http.Header) map[string][]string {
	out := make(map[string][]string, len(h))
	for k, v := range h {
		if !denied(panelOnlyRequestHeaders, k) {
			out[k] = append([]string(nil), v...)
		}
	}
	return out
}

// applySeamRequest puts what the plugins made of the request into the request the handler will see.
func applySeamRequest(r *http.Request, sent, got SeamRequest) SeamRequest {
	r.URL.RawQuery = got.Query
	for name := range sent.Headers {
		if _, kept := got.Headers[name]; !kept {
			r.Header.Del(name)
		}
	}
	for name, values := range got.Headers {
		if !denied(panelOnlyRequestHeaders, name) {
			r.Header[name] = append([]string(nil), values...)
		}
	}
	if !bytes.Equal(sent.Body, got.Body) {
		r.ContentLength = int64(len(got.Body))
		r.Header.Del("Content-Length")
	}
	got.Engine, got.Method, got.Route, got.Path = sent.Engine, sent.Method, sent.Route, sent.Path
	return got
}

// writeSeamResponse sends a response a plugin produced instead of the panel's.
func writeSeamResponse(c *gin.Context, resp SeamResponse) {
	h := c.Writer.Header()
	for name, values := range resp.Headers {
		if !denied(panelOnlyResponseHeaders, name) && !managedResponseHeaders[strings.ToLower(name)] {
			h[name] = append([]string(nil), values...)
		}
	}
	h.Del("Content-Length")
	c.Writer.WriteHeader(resp.Status)
	if c.Request.Method != http.MethodHead && bodyAllowed(resp.Status) {
		_, _ = c.Writer.Write(resp.Body)
	}
}

func bodyAllowed(status int) bool {
	return status != http.StatusNoContent && status != http.StatusNotModified
}

// captureWriter holds the handler's answer back so the after seams can change it. An answer larger
// than a seam may see is let through as it is.
type captureWriter struct {
	gin.ResponseWriter
	status      int
	wroteHeader bool
	buf         bytes.Buffer
	passthrough bool
}

func (w *captureWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status, w.wroteHeader = code, true
	}
}

func (w *captureWriter) WriteHeaderNow() {}

func (w *captureWriter) Status() int {
	if w.wroteHeader {
		return w.status
	}
	return http.StatusOK
}

func (w *captureWriter) Written() bool { return w.wroteHeader || w.passthrough }

func (w *captureWriter) Write(b []byte) (int, error) {
	if w.passthrough {
		return w.ResponseWriter.Write(b)
	}
	if !w.wroteHeader {
		w.status, w.wroteHeader = http.StatusOK, true
	}
	if w.buf.Len()+len(b) > MaxSeamResponseBody {
		w.letThrough()
		return w.ResponseWriter.Write(b)
	}
	return w.buf.Write(b)
}

func (w *captureWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }

func (w *captureWriter) Flush() {
	if w.passthrough {
		w.ResponseWriter.Flush()
	}
}

// letThrough gives up buffering: what was held goes out now and the rest follows directly.
func (w *captureWriter) letThrough() {
	w.passthrough = true
	w.ResponseWriter.WriteHeader(w.Status())
	_, _ = w.ResponseWriter.Write(w.buf.Bytes())
	w.buf.Reset()
}

func runWithAfterSeams(ctx context.Context, c *gin.Context, link *Link, req SeamRequest) {
	orig := c.Writer
	cw := &captureWriter{ResponseWriter: orig}
	c.Writer = cw
	c.Next()
	c.Writer = orig
	if cw.passthrough {
		return
	}
	status := cw.Status()
	body := cw.buf.Bytes()
	shown := map[string][]string{}
	for k, v := range orig.Header() {
		if !managedResponseHeaders[strings.ToLower(k)] {
			shown[k] = append([]string(nil), v...)
		}
	}
	finish := func(status int, body []byte) {
		orig.WriteHeader(status)
		if c.Request.Method != http.MethodHead && bodyAllowed(status) {
			_, _ = orig.Write(body)
		}
	}
	res, err := link.SeamPost(ctx, req, SeamResponse{Status: status, Headers: shown, Body: body})
	if err != nil || !res.Changed {
		finish(status, body)
		return
	}
	h := orig.Header()
	for name := range shown {
		if !denied(panelOnlyResponseHeaders, name) {
			h.Del(name) // the panel's own security headers stay whatever a plugin returned
		}
	}
	for name, values := range res.Response.Headers {
		if !denied(panelOnlyResponseHeaders, name) && !managedResponseHeaders[strings.ToLower(name)] {
			h[name] = append([]string(nil), values...)
		}
	}
	h.Del("Content-Length")
	finish(res.Response.Status, res.Response.Body)
}
