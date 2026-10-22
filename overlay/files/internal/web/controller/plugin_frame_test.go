// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package controller

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/lucx/plugins"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// frameHarness mounts the plugin API (behind login in production) and the page route (without any).
func frameHarness(t *testing.T, m *plugins.Manager) *pluginsHarness {
	t.Helper()
	return frameHarnessWith(t, &service.PluginService{Provider: func() (*plugins.Manager, error) { return m, nil }})
}

func frameHarnessWith(t *testing.T, svc *service.PluginService) *pluginsHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(sessions.Sessions("3x-ui", cookie.NewStore([]byte("plugins-test-secret"))))
	NewPluginsController(engine.Group("/panel/api/plugins"), svc)
	NewPluginFrameController(engine.Group(""), svc)
	return &pluginsHarness{engine: engine}
}

func installUIPlugin(t *testing.T, h *pluginsHarness, enable bool) {
	t.Helper()
	installFrom(t, h, "https://github.com/acme/page", "page-a", enable)
}

func installFrom(t *testing.T, h *pluginsHarness, url, id string, enable bool) {
	t.Helper()
	p := h.call(t, http.MethodPost, "/panel/api/plugins/preview", map[string]string{"url": url})
	var pv plugins.Preview
	if err := json.Unmarshal(p.Obj, &pv); err != nil || !p.Success {
		t.Fatalf("preview: %s %v", p.Msg, err)
	}
	if r := h.call(t, http.MethodPost, "/panel/api/plugins/install", map[string]any{"token": pv.Token, "permissions": pv.Manifest.Permissions}); !r.Success {
		t.Fatalf("install: %s", r.Msg)
	}
	if enable {
		if r := h.call(t, http.MethodPost, "/panel/api/plugins/enable/"+id, nil); !r.Success {
			t.Fatal(r.Msg)
		}
	}
}

func mintToken(t *testing.T, h *pluginsHarness, id string) (string, bool) {
	t.Helper()
	r := h.call(t, http.MethodGet, "/panel/api/plugins/frame-token/"+id, nil)
	if !r.Success {
		return "", false
	}
	var out struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expiresAt"`
	}
	if err := json.Unmarshal(r.Obj, &out); err != nil || out.Token == "" || out.ExpiresAt == "" {
		t.Fatalf("frame token = %s (%v)", r.Obj, err)
	}
	return out.Token, true
}

func TestPluginPagesLoadWithoutASessionOnlyThroughTheirToken(t *testing.T) {
	m := newUIManager(t)
	h := frameHarness(t, m)
	installUIPlugin(t, h, true)
	token, ok := mintToken(t, h, "page-a")
	if !ok {
		t.Fatal("no token for an enabled interface plugin")
	}

	get := func(path string) *httptest.ResponseRecorder {
		return rawCall(h, http.MethodGet, "/panel/plugin-frame/"+token+path, "")
	}
	page := get("/ui/index.html")
	if page.Code != http.StatusOK || page.Body.String() != "<h1>page a</h1>" {
		t.Fatalf("page = %d %q", page.Code, page.Body.String())
	}
	if csp := page.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox allow-scripts") {
		t.Errorf("CSP = %q", csp)
	}
	if rec := get("/ui/replace/clients/"); rec.Code != http.StatusOK || rec.Body.String() != "<h1>clients</h1>" {
		t.Errorf("replacement page = %d %q", rec.Code, rec.Body.String())
	}
	if rec := rawCall(h, http.MethodHead, "/panel/plugin-frame/"+token+"/ui/index.html", ""); rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Errorf("HEAD = %d %q", rec.Code, rec.Body.String())
	}

	for name, rec := range map[string]*httptest.ResponseRecorder{
		"outside ui/":          get("/notes.txt"),
		"the plugin API":       get("/api/ping"),
		"traversal":            get("/ui/../notes.txt"),
		"a POST":               rawCall(h, http.MethodPost, "/panel/plugin-frame/"+token+"/ui/index.html", "{}"),
		"a forged token":       rawCall(h, http.MethodGet, "/panel/plugin-frame/"+plugins.FrameTokenPrefix+"AAAA.AAAA/ui/index.html", ""),
		"no token":             rawCall(h, http.MethodGet, "/panel/plugin-frame//ui/index.html", ""),
		"someone else's token": rawCall(h, http.MethodGet, "/panel/plugin-frame/"+strings.Replace(token, "lxf.", "lxp.", 1)+"/ui/index.html", ""),
	} {
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "page a") {
			t.Errorf("%s: %d %q, want a bare 404", name, rec.Code, rec.Body.String())
		}
	}
}

func TestPageTokenStopsWorkingWhenThePluginIsSwitchedOff(t *testing.T) {
	m := newUIManager(t)
	h := frameHarness(t, m)
	installUIPlugin(t, h, true)
	token, _ := mintToken(t, h, "page-a")
	if rec := rawCall(h, http.MethodGet, "/panel/plugin-frame/"+token+"/ui/index.html", ""); rec.Code != http.StatusOK {
		t.Fatalf("page = %d", rec.Code)
	}
	if r := h.call(t, http.MethodPost, "/panel/api/plugins/disable/page-a", nil); !r.Success {
		t.Fatal(r.Msg)
	}
	if rec := rawCall(h, http.MethodGet, "/panel/plugin-frame/"+token+"/ui/index.html", ""); rec.Code != http.StatusNotFound {
		t.Errorf("page of a disabled plugin = %d, want 404", rec.Code)
	}
}

func TestPageTokensAreOnlyIssuedForPluginsThatShowAPage(t *testing.T) {
	m := newUIManager(t)
	h := frameHarness(t, m)
	installUIPlugin(t, h, false)
	if _, ok := mintToken(t, h, "page-a"); ok {
		t.Error("a token was issued for a disabled plugin")
	}
	if _, ok := mintToken(t, h, "nope"); ok {
		t.Error("a token was issued for an unknown plugin")
	}
}

func TestAProcessPluginPageComesThroughTheTokenButNotItsAPI(t *testing.T) {
	manifest := `{"id":"page-p","name":"Page P","version":"1.0.0","apiVersion":"1","type":"process","run":{"command":"bin/${arch}/alerts"},` +
		`"permissions":["ui:menu"],"ui":{"menu":[{"title":"P","path":"/ui/index.html"}]}}`
	m := newManagerServing(t, pluginsArchiveWithManifest(t, manifest))
	sockDir, err := os.MkdirTemp("", "lucxp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	runner := newStubRunner()
	runner.socketDir = sockDir
	sup := plugins.NewSupervisor(m, runner, plugins.SupervisorOptions{GOARCH: "amd64", Health: func(context.Context, string) error { return nil }})
	h := frameHarnessWith(t, &service.PluginService{
		Provider:   func() (*plugins.Manager, error) { return m, nil },
		Supervisor: func() *plugins.Supervisor { return sup },
	})
	installFrom(t, h, "https://github.com/acme/alerts", "page-p", true)
	sup.Reconcile(context.Background())

	var mu sync.Mutex
	var seen []string
	ln, err := net.Listen("unix", filepath.Join(sockDir, "page-p.sock"))
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path+" as "+r.Header.Get("X-Lucx-User"))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<h1>process page</h1>"))
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	token, ok := mintToken(t, h, "page-p")
	if !ok {
		t.Fatal("no token for a process plugin that has a menu entry")
	}
	rec := rawCall(h, http.MethodGet, "/panel/plugin-frame/"+token+"/ui/index.html", "")
	if rec.Code != http.StatusOK || rec.Body.String() != "<h1>process page</h1>" {
		t.Fatalf("page = %d %q", rec.Code, rec.Body.String())
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "connect-src 'none'") {
		t.Errorf("CSP = %q", csp)
	}
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/ping"},
		{http.MethodGet, "/_lucx/health"},
		{http.MethodPost, "/ui/index.html"},
		{http.MethodGet, "/ui/../api/ping"},
	} {
		if r := rawCall(h, c.method, "/panel/plugin-frame/"+token+c.path, "x"); r.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", c.method, c.path, r.Code)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || seen[0] != "GET /ui/index.html as api-token" {
		t.Errorf("the process saw %v, want only the page request", seen)
	}
}
