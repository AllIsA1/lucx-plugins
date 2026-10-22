// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostapi

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/AllIsA1/lucx-plugins/internal/plugins"
)

const (
	maxGatewayBody     = 10 << 20
	gatewayTimeout     = 60 * time.Second
	headerPluginID     = "X-LucX-Plugin-Id"
	headerAllowedPaths = "X-LucX-Allowed-Routes"
)

// Gateway is how plugins reach the panel's API: they present their own token here, the host decides
// what that plugin may call, and the request goes on to the panel's internal API socket as the
// administrator. The panel enforces the list this gateway sends against the route it resolves.
type Gateway struct {
	svc    *PluginService
	panel  *http.Transport
	engine *gin.Engine
}

// NewGateway forwards to the panel's internal API on the unix socket panelSocket.
func NewGateway(svc *PluginService, panelSocket string) *Gateway {
	g := &Gateway{
		svc: svc,
		panel: &http.Transport{
			DisableKeepAlives:     true,
			ResponseHeaderTimeout: gatewayTimeout,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", panelSocket)
			},
		},
	}
	gin.SetMode(gin.ReleaseMode)
	g.engine = gin.New()
	g.engine.RedirectTrailingSlash = false
	g.engine.RedirectFixedPath = false
	g.engine.Use(gin.Recovery())
	g.engine.Any("/panel/api/*path", g.forward)
	return g
}

// Handler is the http.Handler to serve on the gateway's loopback address.
func (g *Gateway) Handler() http.Handler { return g.engine }

func bearer(c *gin.Context) string {
	const prefix = "Bearer "
	h := c.GetHeader("Authorization")
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}

func formatRoutes(routes []plugins.APIRoute) string {
	parts := make([]string, len(routes))
	for i, r := range routes {
		parts[i] = r.Method + " " + r.Pattern
	}
	return strings.Join(parts, ",")
}

func (g *Gateway) forward(c *gin.Context) {
	m, err := g.svc.Provider()
	if err != nil {
		pureJsonMsg(c, http.StatusServiceUnavailable, false, "the plugin system is unavailable")
		return
	}
	entry, ok := m.VerifyToken(bearer(c))
	if !ok {
		c.Status(http.StatusUnauthorized)
		return
	}
	allowed := plugins.AllowedRoutes(entry.Permissions)
	if len(allowed) == 0 {
		pureJsonMsg(c, http.StatusForbidden, false, "this plugin holds no API permission")
		return
	}
	proxy := &httputil.ReverseProxy{
		Transport: g.panel,
		Rewrite: func(pr *httputil.ProxyRequest) {
			out := pr.Out
			out.URL.Scheme, out.URL.Host, out.Host = "http", "panel", "panel"
			for _, h := range []string{"Authorization", "Cookie", "Proxy-Authorization", "X-Csrf-Token", headerUser, headerScope} {
				out.Header.Del(h)
			}
			// Both are set by the host and by nobody else: a plugin cannot name itself or widen its list.
			out.Header.Set(headerPluginID, entry.ID)
			out.Header.Set(headerAllowedPaths, formatRoutes(allowed))
		},
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Del("Set-Cookie")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			status, msg := http.StatusBadGateway, "the panel did not answer: "+err.Error()
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				status, msg = http.StatusRequestEntityTooLarge, "the request body is too large"
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"success":false,"msg":"`+msg+`","obj":null}`)
		},
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxGatewayBody)
	proxy.ServeHTTP(c.Writer, c.Request)
}
