// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/lucx/plugins"
)

func pluginPagesRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(SecurityHeadersMiddleware(false))
	// The reverse proxy copies upstream headers with Add, so a header the panel already set would double.
	page := func(c *gin.Context) {
		upstream := http.Header{}
		plugins.SetPageHeaders(upstream)
		for name, values := range upstream {
			for _, v := range values {
				c.Writer.Header().Add(name, v)
			}
		}
		c.String(http.StatusOK, "page")
	}
	plain := func(c *gin.Context) { c.String(http.StatusOK, "ok") }
	r.GET("/panel/api/plugins/x/:id/*path", page)
	r.GET("/secret/panel/api/plugins/x/:id/*path", page)
	r.GET("/panel/plugin-frame/:token/*path", page)
	r.GET("/secret/panel/plugin-frame/:token/*path", page)
	r.GET("/panel/api/plugins/list", plain)
	r.GET("/panel/api/plugins/xray/:id", plain)
	r.GET("/panel/api/inbounds/list", plain)
	return r
}

func get(r *gin.Engine, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func TestPluginPagesMayBeFramedByThePanelOnly(t *testing.T) {
	r := pluginPagesRouter()
	for _, path := range []string{
		"/panel/api/plugins/x/page-a/ui/index.html", "/secret/panel/api/plugins/x/page-a/ui/index.html",
		"/panel/plugin-frame/lxf.a.b/ui/index.html", "/secret/panel/plugin-frame/lxf.a.b/ui/index.html",
	} {
		h := get(r, path).Header()
		if got := h.Values("X-Frame-Options"); len(got) != 1 || got[0] != "SAMEORIGIN" {
			t.Errorf("%s: X-Frame-Options = %q, want one SAMEORIGIN", path, got)
		}
		csp := h.Values("Content-Security-Policy")
		if len(csp) != 1 || !strings.Contains(csp[0], "sandbox allow-scripts") || strings.Contains(csp[0], "nonce-") {
			t.Errorf("%s: CSP = %q, want only the plugin page policy", path, csp)
		}
		if h.Get("Referrer-Policy") != "no-referrer" || h.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: hardening headers lost: %v", path, h)
		}
	}
}

func TestEveryOtherPanelRouteStaysUnframeable(t *testing.T) {
	r := pluginPagesRouter()
	for _, path := range []string{"/panel/api/plugins/list", "/panel/api/inbounds/list", "/panel/api/plugins/xray/x", "/nope"} {
		h := get(r, path).Header()
		if h.Get("X-Frame-Options") != "DENY" {
			t.Errorf("%s: X-Frame-Options = %q, want DENY", path, h.Get("X-Frame-Options"))
		}
		if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
			t.Errorf("%s: CSP = %q, want the panel policy", path, csp)
		}
	}
}
