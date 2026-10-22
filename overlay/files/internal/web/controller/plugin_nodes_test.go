// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/lucx/nodetype"
	"github.com/mhsanaei/3x-ui/v3/internal/lucx/plugins"
	"github.com/mhsanaei/3x-ui/v3/internal/web/runtime"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

type nodeClientSpy struct {
	listed    []runtime.RemotePlugin
	calls     []string
	installed runtime.RemotePluginInstall
	err       error
}

func (s *nodeClientSpy) PluginsList(context.Context) (*runtime.RemotePluginsState, error) {
	s.calls = append(s.calls, "list")
	return &runtime.RemotePluginsState{Available: true, AllowMasterManagement: true, Plugins: s.listed}, nil
}

func (s *nodeClientSpy) PluginsInstall(_ context.Context, req runtime.RemotePluginInstall) error {
	s.calls = append(s.calls, "install")
	s.installed = req
	return s.err
}

func (s *nodeClientSpy) PluginsSetEnabled(_ context.Context, id string, enabled bool) error {
	s.calls = append(s.calls, "enabled="+strconv.FormatBool(enabled)+" "+id)
	return s.err
}

func (s *nodeClientSpy) PluginsUninstall(_ context.Context, id string, removeData bool) error {
	s.calls = append(s.calls, "uninstall data="+strconv.FormatBool(removeData)+" "+id)
	return s.err
}

func (s *nodeClientSpy) PluginsRestart(_ context.Context, id string) error {
	s.calls = append(s.calls, "restart "+id)
	return s.err
}

func (s *nodeClientSpy) PluginsLogs(_ context.Context, id string, lines int) ([]string, error) {
	s.calls = append(s.calls, "logs "+id+" "+strconv.Itoa(lines))
	return []string{"hello"}, s.err
}

func newPluginNodesHarness(t *testing.T, scope string) (*pluginsHarness, *nodeClientSpy, int) {
	t.Helper()
	dbDir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbDir)
	dbtest.InitDB(t, filepath.Join(dbDir, "x-ui.db"))
	node := &model.Node{
		Name: "edge", Scheme: "https", Address: "edge.example.com", Port: 2053, Enable: true, Status: "online",
		Features: (&nodetype.NodeInfo{NodeType: nodetype.TypeLucX, Features: []string{nodetype.FeaturePlugins}}).ToJSON(),
	}
	if err := database.GetDB().Create(node).Error; err != nil {
		t.Fatal(err)
	}

	spy := &nodeClientSpy{}
	entry := plugins.Entry{
		ID: "blockads", Version: "1.0.0", Type: plugins.TypeProcess, Scope: plugins.ScopeBoth,
		Source: plugins.Source{Owner: "acme", Repo: "blockads"}, Commit: pluginsTestCommit,
		ManifestSHA256: strings.Repeat("ab", 32), Permissions: []string{"network"},
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(sessions.Sessions("3x-ui", cookie.NewStore([]byte("plugins-test-secret"))))
	api := engine.Group("/panel/api")
	api.Use(func(c *gin.Context) {
		if scope != "" {
			c.Set("api_authed", true)
			c.Set("api_token_scope", scope)
		}
		c.Next()
	})
	// The opt-in is on: only the route allowlist may keep a node-sync token out.
	mgr := newRealManager(t)
	if err := mgr.SetAllowMasterManagement(true); err != nil {
		t.Fatal(err)
	}
	local := &service.PluginService{Provider: func() (*plugins.Manager, error) { return mgr, nil }}
	a := NewPluginsController(api.Group("/plugins"), local)
	a.nodes = service.NewNodePluginsServiceFor(
		func() ([]plugins.Entry, error) { return []plugins.Entry{entry}, nil },
		func(*model.Node) (service.NodePluginsClient, error) { return spy, nil },
	)
	return &pluginsHarness{engine: engine}, spy, node.Id
}

func TestNodePluginRoutesDriveTheNodeService(t *testing.T) {
	h, spy, id := newPluginNodesHarness(t, "")
	base := "/plugins/nodes/" + strconv.Itoa(id)

	r := h.callAt(t, http.MethodGet, "/plugins/nodes", nil)
	var statuses []service.NodePluginStatus
	if err := json.Unmarshal(r.Obj, &statuses); err != nil || !r.Success || len(statuses) != 1 || statuses[0].NodeName != "edge" {
		t.Fatalf("list = %+v (%v) %s", r, err, r.Obj)
	}

	if r := h.callAt(t, http.MethodPost, base+"/install", map[string]any{"id": "blockads", "permissions": []string{"network"}}); !r.Success {
		t.Fatalf("install: %s", r.Msg)
	}
	if spy.installed.URL != "https://github.com/acme/blockads" || spy.installed.Commit != pluginsTestCommit {
		t.Errorf("node was told %+v, want the master's own pinned plugin", spy.installed)
	}
	if r := h.callAt(t, http.MethodPost, base+"/install", map[string]any{"id": "blockads", "permissions": []string{"network", "api:nodes:write"}}); r.Success || !strings.Contains(r.Msg, "differ") {
		t.Errorf("install with other permissions = %+v, want a refusal that says so", r)
	}

	steps := []struct {
		method, path string
		body         any
		want         string
	}{
		{http.MethodPost, base + "/enable/blockads", nil, "enabled=true blockads"},
		{http.MethodPost, base + "/disable/blockads", nil, "enabled=false blockads"},
		{http.MethodPost, base + "/restart/blockads", nil, "restart blockads"},
		{http.MethodPost, base + "/uninstall/blockads", map[string]bool{"removeData": true}, "uninstall data=true blockads"},
		{http.MethodPost, base + "/uninstall/blockads", nil, "uninstall data=false blockads"},
		{http.MethodGet, base + "/logs/blockads?lines=20", nil, "logs blockads 20"},
		{http.MethodGet, base + "/logs/blockads", nil, "logs blockads 200"},
	}
	for _, st := range steps {
		spy.calls = nil
		if r := h.callAt(t, st.method, st.path, st.body); !r.Success {
			t.Fatalf("%s %s: %s", st.method, st.path, r.Msg)
		}
		if len(spy.calls) == 0 || spy.calls[0] != st.want {
			t.Errorf("%s %s sent %v, want %q first", st.method, st.path, spy.calls, st.want)
		}
	}

	spy.err = errors.New("plugin management by the master is disabled on this node")
	if r := h.callAt(t, http.MethodPost, base+"/restart/blockads", nil); r.Success || !strings.Contains(r.Msg, "disabled on this node") {
		t.Errorf("a refusing node = %+v, want its reason shown", r)
	}
	if r := h.callAt(t, http.MethodPost, "/plugins/nodes/9999/restart/blockads", nil); r.Success || !strings.Contains(r.Msg, "node not found") {
		t.Errorf("unknown node = %+v", r)
	}
	if r := h.callAt(t, http.MethodPost, "/plugins/nodes/abc/restart/blockads", nil); r.Success {
		t.Error("a non-numeric node id was accepted")
	}
	if r := h.callAt(t, http.MethodGet, base+"/logs/blockads?lines=0", nil); r.Success {
		t.Error("lines=0 was accepted")
	}
}

func TestANodeCannotManagePluginsOnOtherNodes(t *testing.T) {
	h, spy, id := newPluginNodesHarness(t, model.ApiScopeNodeSync)
	base := "/plugins/nodes/" + strconv.Itoa(id)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/plugins/nodes"},
		{http.MethodPost, base + "/install"},
		{http.MethodPost, base + "/enable/blockads"},
		{http.MethodPost, base + "/update/blockads"},
		{http.MethodGet, base + "/logs/blockads"},
	} {
		if r := h.callAt(t, c.method, c.path, map[string]any{"id": "blockads", "permissions": []string{"network"}}); r.Success {
			t.Errorf("%s %s must not be reachable with a node-sync token", c.method, c.path)
		}
	}
	if len(spy.calls) != 0 {
		t.Errorf("node clients were used on behalf of a node: %v", spy.calls)
	}
}

func TestNodeUpdateRouteReinstallsThePinnedCommit(t *testing.T) {
	h, spy, id := newPluginNodesHarness(t, "")
	spy.listed = []runtime.RemotePlugin{{ID: "blockads", Commit: strings.Repeat("f", 40), ManagedByMaster: true, Enabled: true}}
	base := "/plugins/nodes/" + strconv.Itoa(id)

	if r := h.callAt(t, http.MethodPost, base+"/update/blockads", map[string]any{"permissions": []string{"network", "api:nodes:write"}}); r.Success || !strings.Contains(r.Msg, "differ") {
		t.Errorf("update with other permissions = %+v", r)
	}
	if got := strings.Join(spy.calls, ","); strings.Contains(got, "uninstall") {
		t.Fatalf("the node was touched by a refused update: %s", got)
	}

	spy.calls = nil
	if r := h.callAt(t, http.MethodPost, base+"/update/blockads", map[string]any{"permissions": []string{"network"}}); !r.Success {
		t.Fatalf("update: %s", r.Msg)
	}
	want := []string{"list", "uninstall data=false blockads", "install", "enabled=true blockads"}
	if got := spy.calls[:len(want)]; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("node calls = %v, want %v first", spy.calls, want)
	}
	if spy.installed.Commit != pluginsTestCommit {
		t.Errorf("installed %+v, want the master's commit", spy.installed)
	}
	if r := h.callAt(t, http.MethodPost, base+"/update/blockads", "not json"); r.Success {
		t.Error("a malformed body was accepted")
	}
}
