// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package sub

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSeamRoutesFollowTheConfiguredPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resolve := seamRouteResolver("/s/", "/j/", "/c/", "/a/")
	e := gin.New()
	got := map[string]string{}
	e.Use(func(c *gin.Context) { c.Next(); got[c.Request.URL.Path] = resolve(c) })
	h := func(c *gin.Context) { c.Status(200) }
	for _, p := range []string{"/s/:subid", "/s/:subid/hwid-status", "/j/:subid", "/c/:subid", "/mihomo/:subid", "/clash-legacy/:subid", "/a/:subid", "/assets/:file"} {
		e.GET(p, h)
	}
	want := map[string]string{
		"/s/x": SeamRouteLinks, "/s/x/hwid-status": SeamRouteLinksHWID, "/j/x": SeamRouteJSON, "/c/x": SeamRouteClash,
		"/mihomo/x": SeamRouteMihomo, "/clash-legacy/x": SeamRouteClashLegacy, "/a/x": SeamRouteAwg, "/assets/x": "",
	}
	for path := range want {
		e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}
	for path, name := range want {
		if got[path] != name {
			t.Errorf("%s resolved to %q, want %q", path, got[path], name)
		}
	}
}

func TestTheRootSubscriptionPathAndAClashPathThatIsTheMihomoAlias(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resolve := seamRouteResolver("/", "/json/", "/mihomo/", "/awg/")
	e := gin.New()
	var got string
	e.GET("/:subid", func(c *gin.Context) { got = resolve(c) })
	e.GET("/mihomo/:subid", func(c *gin.Context) { got += "|" + resolve(c) })
	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/abc", nil))
	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/mihomo/abc", nil))
	if got != SeamRouteLinks+"|"+SeamRouteClash {
		t.Errorf("= %q: the configured clash path must win over the alias", got)
	}
}
