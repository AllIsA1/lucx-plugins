// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package controller

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/lucx/plugins"
)

// pluginAuthEngine is the production auth wiring plus the routes plugins may or
// may not reach, with a real plugin manager holding one enabled plugin.
func pluginAuthEngine(t *testing.T, perms []string) (*gin.Engine, string) {
	t.Helper()
	engine, a := newAPIAuthTestEngine(t)

	root := t.TempDir()
	cfg := plugins.Config{
		CodeDir:      filepath.Join(root, "plugins"),
		DataDir:      filepath.Join(root, "data"),
		RegistryPath: filepath.Join(root, "plugins.json"),
		Runner:       plugins.NewUnavailableRunner("tests"),
	}
	entry := plugins.Entry{
		ID: "traffic-alerts", Name: "Traffic Alerts", Version: "1.0.0", Type: plugins.TypeProcess, Scope: plugins.ScopeMaster,
		Commit: "0123456789abcdef0123456789abcdef01234567", Enabled: true, Permissions: perms,
		InstalledAt: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC),
	}
	raw, err := json.Marshal(map[string]any{"schema": 1, "plugins": []plugins.Entry{entry}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.RegistryPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XUI_PLUGINS_DISABLED", "")
	plugins.Start(cfg)
	t.Cleanup(plugins.Stop)
	m, err := plugins.Get()
	if err != nil {
		t.Fatal(err)
	}

	api := engine.Group("/panel/api")
	api.Use(a.checkAPIAuth)
	api.Use(a.enforceTokenScope)
	ok := func(c *gin.Context) {
		scope, _ := c.Get("api_token_scope")
		id, _ := c.Get("plugin_id")
		c.JSON(http.StatusOK, gin.H{"reached": true, "scope": scope, "plugin": id})
	}
	api.GET("/clients/list", ok)
	api.GET("/clients/get/:email", ok)
	api.POST("/clients/add", ok)
	api.GET("/inbounds/list", ok)
	api.GET("/plugins/list", ok)
	api.POST("/plugins/install", ok)
	api.GET("/server/getDb", ok)
	api.POST("/setting/updateUser", ok)
	return engine, m.IssueToken(m.List()[0])
}

func callWithToken(engine *gin.Engine, method, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	return w
}

func TestPluginTokenReachesOnlyTheRoutesItsPermissionsName(t *testing.T) {
	engine, token := pluginAuthEngine(t, []string{"api:clients:read", "events:client.created"})

	for _, path := range []string{"/panel/api/clients/list", "/panel/api/clients/get/alice@example.com"} {
		w := callWithToken(engine, http.MethodGet, path, token)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200: %s", path, w.Code, w.Body.String())
		}
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if body["scope"] != plugins.TokenScope || body["plugin"] != "traffic-alerts" {
			t.Errorf("GET %s: scope/plugin = %v / %v", path, body["scope"], body["plugin"])
		}
	}
	denied := []struct{ method, path string }{
		{http.MethodPost, "/panel/api/clients/add"},
		{http.MethodGet, "/panel/api/inbounds/list"},
		{http.MethodGet, "/panel/api/plugins/list"},
		{http.MethodPost, "/panel/api/plugins/install"},
		{http.MethodGet, "/panel/api/server/getDb"},
		{http.MethodPost, "/panel/api/setting/updateUser"},
	}
	for _, d := range denied {
		if w := callWithToken(engine, d.method, d.path, token); w.Code != http.StatusForbidden {
			t.Errorf("%s %s = %d, want 403", d.method, d.path, w.Code)
		}
	}
}

func TestPluginTokenWithoutPermissionsReachesNothing(t *testing.T) {
	engine, token := pluginAuthEngine(t, nil)
	if w := callWithToken(engine, http.MethodGet, "/panel/api/clients/list", token); w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestPluginTokenForgeriesAreRejectedBeforeAnyFallback(t *testing.T) {
	engine, token := pluginAuthEngine(t, []string{"api:clients:read"})
	for name, tok := range map[string]string{
		"tampered signature": token[:len(token)-2] + "AA",
		"unknown plugin":     plugins.TokenPrefix + "ghost-plugin.AAAA",
		"bare prefix":        plugins.TokenPrefix,
	} {
		if w := callWithToken(engine, http.MethodGet, "/panel/api/clients/list", tok); w.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", name, w.Code)
		}
	}
}

func TestPluginTokenStopsWorkingWhenThePluginIsDisabled(t *testing.T) {
	engine, token := pluginAuthEngine(t, []string{"api:clients:read"})
	m, err := plugins.Get()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetEnabled("traffic-alerts", false); err != nil {
		t.Fatal(err)
	}
	if w := callWithToken(engine, http.MethodGet, "/panel/api/clients/list", token); w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for a disabled plugin", w.Code)
	}
}

// The static node-sync allowlist hands every /plugins/* route to the plugin
// controller, which applies its own narrow list and the node owner's opt-in;
// routes outside the plugin API stay governed by the static list.
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

// Masters learn a node's features (plugins included) from GET /lucx/hello with
// the enrollment token. Refused for node-sync, they would fall back to features
// inferred from the version and never see a node that supports plugins.
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
