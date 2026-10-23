// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package controller

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/lucx/hostlink"
	"github.com/mhsanaei/3x-ui/v3/internal/web/middleware"
)

// hostEngine is the production auth wiring (login check, token scopes, CSRF) with a few routes, seen
// from both listeners: public is what browsers reach, internal is the plugin host's socket.
func hostEngine(t *testing.T) (public, internal http.Handler, served *[]string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dbDir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbDir)
	dbtest.InitDB(t, filepath.Join(dbDir, "x-ui.db"))
	engine := gin.New()
	engine.Use(sessions.Sessions("3x-ui", cookie.NewStore([]byte("host-auth-test-secret"))))
	a := &APIController{}
	var log []string
	api := engine.Group("/panel/api")
	api.Use(a.checkAPIAuth)
	api.Use(a.enforceTokenScope)
	api.Use(middleware.CSRFMiddleware())
	ok := func(c *gin.Context) {
		log = append(log, c.Request.Method+" "+c.FullPath())
		c.JSON(http.StatusOK, gin.H{"reached": true, "authed": c.GetBool("api_authed"), "plugin": c.GetString("plugin_id")})
	}
	api.GET("/clients/list", ok)
	api.POST("/clients/add", ok)
	api.GET("/inbounds/list", ok)
	api.POST("/server/updatePanel", ok)
	api.GET("/server/getDb", ok)
	return engine, hostlink.InternalHandler(engine, func() string { return "" }), &log
}

func hostCall(h http.Handler, method, path, allowed string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("X-LucX-Allowed-Routes", allowed)
	req.Header.Set("X-LucX-Plugin-Id", "billing")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestTheHostsSocketReachesOnlyTheRoutesItNamesAndNeedsNoSessionOrCsrf(t *testing.T) {
	_, internal, served := hostEngine(t)
	allowed := "GET /clients/list,POST /clients/add"

	for _, c := range []struct{ method, path string }{{http.MethodGet, "/panel/api/clients/list"}, {http.MethodPost, "/panel/api/clients/add"}} {
		w := hostCall(internal, c.method, c.path, allowed)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"authed":true`) || !strings.Contains(w.Body.String(), `"plugin":"billing"`) {
			t.Errorf("%s %s = %d %s, want the handler to run as an authenticated call for the plugin", c.method, c.path, w.Code, w.Body)
		}
	}
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/panel/api/inbounds/list"},
		{http.MethodPost, "/panel/api/server/updatePanel"},
		{http.MethodGet, "/panel/api/server/getDb"},
		{http.MethodGet, "/panel/api/clients/add"},
	} {
		if w := hostCall(internal, c.method, c.path, allowed); w.Code != http.StatusForbidden && w.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want it refused", c.method, c.path, w.Code)
		}
	}
	if len(*served) != 2 {
		t.Errorf("handlers ran %v, want exactly the two allowed calls", *served)
	}
}

func TestTheSameHeadersOnThePublicListenerOpenNothing(t *testing.T) {
	public, _, served := hostEngine(t)
	for _, path := range []string{"/panel/api/clients/list", "/panel/api/server/getDb"} {
		w := hostCall(public, http.MethodGet, path, "GET /clients/list,GET /server/getDb")
		if w.Code == http.StatusOK {
			t.Errorf("GET %s with host headers on the public listener = 200", path)
		}
	}
	if len(*served) != 0 {
		t.Errorf("a client posing as the plugin host reached %v", *served)
	}
}

// The static node-sync allowlist hands every /plugins/* route to the plugin host, whose own gate
// applies the narrow list and the node owner's opt-in; routes outside the plugin API stay governed
// by the static list.
func TestNodeSyncReachesThePluginGroupButNothingElse(t *testing.T) {
	engine, a := newAPIAuthTestEngine(t)
	api := engine.Group("/panel/api")
	api.Use(a.checkAPIAuth)
	api.Use(a.enforceTokenScope)
	reached := func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"reached": true}) }
	api.GET("/plugins/list", reached)
	api.POST("/plugins/settings", reached)
	api.POST("/settings/unlisted", reached)

	do := func(method, path string) int {
		req := httptest.NewRequest(method, path, nil)
		req.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{&x509.Certificate{}}}}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w.Code
	}
	for _, p := range []struct{ method, path string }{{http.MethodGet, "/panel/api/plugins/list"}, {http.MethodPost, "/panel/api/plugins/settings"}} {
		if code := do(p.method, p.path); code != http.StatusOK {
			t.Errorf("%s %s = %d: a node-sync caller must reach the plugin group, whose own gate decides", p.method, p.path, code)
		}
	}
	if code := do(http.MethodPost, "/panel/api/settings/unlisted"); code != http.StatusForbidden {
		t.Errorf("an unlisted route outside the plugin API = %d, want 403", code)
	}
}

// Masters learn a node's features (plugins included) from GET /lucx/hello with the enrollment
// token. Refused for node-sync, they would fall back to features inferred from the version and
// never see a node that supports plugins.
func TestNodeSyncMayAskForTheNodesCapabilities(t *testing.T) {
	engine, a := newAPIAuthTestEngine(t)
	api := engine.Group("/panel/api")
	api.Use(a.checkAPIAuth)
	api.Use(a.enforceTokenScope)
	api.GET("/lucx/hello", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"success": true}) })
	api.POST("/lucx/hello", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"success": true}) })

	do := func(method string) int {
		req := httptest.NewRequest(method, "/panel/api/lucx/hello", nil)
		req.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{&x509.Certificate{}}}}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w.Code
	}
	if code := do(http.MethodGet); code != http.StatusOK {
		t.Errorf("GET /lucx/hello = %d, want 200 for node-sync", code)
	}
	if code := do(http.MethodPost); code != http.StatusForbidden {
		t.Errorf("POST /lucx/hello = %d, want 403: only the read is allowed", code)
	}
}
