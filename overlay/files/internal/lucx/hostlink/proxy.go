// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostlink

import (
	"context"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/web/session"
)

const (
	// proxyTimeout is how long the host may take to answer: a catalog preview downloads and unpacks.
	proxyTimeout = 3*time.Minute + 10*time.Second

	headerUser        = "X-LucX-User"
	headerScope       = "X-LucX-Scope"
	headerIntegration = "X-LucX-Integration"

	// rawWriterKey is middleware.RawWriterKey; a test keeps the two equal.
	rawWriterKey = "lucx_raw_writer"
)

// Route is one panel route that is answered by the host.
type Route struct{ Method, Path string }

// AdminRoutes are the /panel/api/plugins routes the host serves, relative to that prefix. The panel
// registers exactly these, so the API documentation and route checks keep working; a golden file in
// testdata is compared with the host's own table.
var AdminRoutes = []Route{
	{http.MethodGet, "/list"},
	{http.MethodGet, "/menu"},
	{http.MethodGet, "/catalog"},
	{http.MethodPost, "/catalog/refresh"},
	{http.MethodPost, "/catalog/preview/:id"},
	{http.MethodPost, "/catalog/update-preview/:id"},
	{http.MethodPost, "/catalog/settings"},
	{http.MethodGet, "/frame-token/:id"},
	{http.MethodPost, "/preview"},
	{http.MethodPost, "/install"},
	{http.MethodPost, "/remote/install"},
	{http.MethodPost, "/master-management"},
	{http.MethodPost, "/enable/:id"},
	{http.MethodPost, "/disable/:id"},
	{http.MethodPost, "/restart/:id"},
	{http.MethodPost, "/update-preview/:id"},
	{http.MethodPost, "/update/:id"},
	{http.MethodPost, "/rollback/:id"},
	{http.MethodGet, "/logs/:id"},
	{http.MethodGet, "/nodes"},
	{http.MethodPost, "/nodes/:nodeId/refresh"},
	{http.MethodPost, "/nodes/:nodeId/install"},
	{http.MethodPost, "/nodes/:nodeId/update/:id"},
	{http.MethodPost, "/nodes/:nodeId/enable/:id"},
	{http.MethodPost, "/nodes/:nodeId/disable/:id"},
	{http.MethodPost, "/nodes/:nodeId/restart/:id"},
	{http.MethodPost, "/nodes/:nodeId/uninstall/:id"},
	{http.MethodGet, "/nodes/:nodeId/logs/:id"},
	{http.MethodGet, "/system"},
	{http.MethodPost, "/system/check"},
	{http.MethodPost, "/system/update"},
	{http.MethodGet, "/x/:id/*path"},
	{http.MethodPost, "/x/:id/*path"},
	{http.MethodPut, "/x/:id/*path"},
	{http.MethodDelete, "/x/:id/*path"},
	{http.MethodPost, "/uninstall/:id"},
	{http.MethodPost, "/settings"},
}

// RegisterAdmin mounts the plugin admin routes on g, the /panel/api/plugins group. The group's own
// middleware (login, scope, CSRF) has already run when a request gets here.
func (l *Link) RegisterAdmin(g *gin.RouterGroup) {
	h := l.forward(g.BasePath(), "/panel/api/plugins")
	for _, r := range AdminRoutes {
		g.Handle(r.Method, r.Path, h)
	}
}

// RegisterFrame mounts the cookie-less page route of the sandboxed plugin iframe on g, the group that
// carries the panel's base path. The host checks the token in the URL.
func (l *Link) RegisterFrame(g *gin.RouterGroup) {
	h := l.forward(strings.TrimSuffix(g.BasePath(), "/"), "")
	g.GET("/panel/plugin-frame/:token/*path", h)
	g.HEAD("/panel/plugin-frame/:token/*path", h)
}

// hostPath maps the path the browser used (with the panel's base path) to the path the host serves.
func hostPath(escaped, stripPrefix, hostPrefix string) (path, raw string) {
	rest := strings.TrimPrefix(escaped, stripPrefix)
	raw = hostPrefix + rest
	path, err := url.PathUnescape(raw)
	if err != nil {
		path = raw
	}
	return path, raw
}

func (l *Link) forward(stripPrefix, hostPrefix string) gin.HandlerFunc {
	proxy := &httputil.ReverseProxy{
		Transport:     l.slow,
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			out := pr.Out
			out.URL.Scheme, out.URL.Host, out.Host = "http", "host", "host"
			out.URL.Path, out.URL.RawPath = hostPath(pr.In.URL.EscapedPath(), stripPrefix, hostPrefix)
			// Nothing the browser or a plugin sent may pose as the panel's own verdict, and the
			// panel's session never leaves the panel.
			for k := range out.Header {
				if strings.HasPrefix(http.CanonicalHeaderKey(k), "X-Lucx-") {
					out.Header.Del(k)
				}
			}
			for _, h := range []string{"Cookie", "Authorization", "Proxy-Authorization", "X-Csrf-Token"} {
				out.Header.Del(h)
			}
			if v, ok := pr.In.Context().Value(callerKey{}).(caller); ok {
				out.Header.Set(headerUser, v.user)
				out.Header.Set(headerScope, v.scope)
			}
			out.Header.Set(headerIntegration, "1")
		},
		ModifyResponse: func(resp *http.Response) error {
			// A page served from the panel's origin must not be able to set the panel's cookies.
			resp.Header.Del("Set-Cookie")
			// The panel compresses what it sends, so the host's length would be wrong for the body that
			// goes out: browsers cut such a response off.
			resp.Header.Del("Content-Length")
			resp.ContentLength = -1
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			l.unavailable(w, r, err)
		},
	}
	return func(c *gin.Context) {
		extendWriteDeadline(c, proxyTimeout)
		ctx := c.Request.Context()
		c.Request = c.Request.WithContext(withCaller(ctx, callerOf(c)))
		proxy.ServeHTTP(c.Writer, c.Request)
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			l.Kick()
		}
	}
}

type callerKey struct{}

func withCaller(ctx context.Context, v caller) context.Context {
	return context.WithValue(ctx, callerKey{}, v)
}

type caller struct{ user, scope string }

func callerOf(c *gin.Context) caller {
	v := caller{user: "api-token", scope: c.GetString("api_token_scope")}
	if u := session.GetLoginUser(c); u != nil && u.Username != "" {
		v.user = u.Username
	}
	return v
}

// extendWriteDeadline lets this response take up to d: the server's WriteTimeout would otherwise cut it.
func extendWriteDeadline(c *gin.Context, d time.Duration) {
	var w http.ResponseWriter = c.Writer
	if raw, ok := c.Get(rawWriterKey); ok {
		if rw, ok := raw.(http.ResponseWriter); ok {
			w = rw
		}
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(d))
}

// unavailable answers when the host cannot: the menu and the list degrade to "no plugins" (so the
// panel looks as it did without plugins), everything else says why it failed.
func (l *Link) unavailable(w http.ResponseWriter, r *http.Request, cause error) {
	reason := l.Status().Reason
	if reason == "" {
		reason = "the plugin system did not answer: " + cause.Error()
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/panel/api/plugins/menu") {
		_, _ = io.WriteString(w, `{"success":true,"msg":"","obj":{"menu":[],"replaces":{}}}`)
		return
	}
	if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/panel/api/plugins/list") {
		_, _ = io.WriteString(w, `{"success":true,"msg":"","obj":{"available":false,"allowMasterManagement":false,`+
			`"processesAvailable":false,"processesError":"","safeMode":false,"error":`+quote(reason)+
			`,"archiveBase":"","customArchiveBase":"","defaultArchiveBase":"","plugins":[]}}`)
		return
	}
	if strings.Contains(r.URL.Path, "/panel/plugin-frame/") {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_, _ = io.WriteString(w, `{"success":false,"msg":`+quote(reason)+`,"obj":null}`)
}
