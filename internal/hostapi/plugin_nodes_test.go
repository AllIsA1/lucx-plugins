// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/AllIsA1/lucx-plugins/internal/plugins"
)

type nodeClientSpy struct {
	mu        sync.Mutex
	listed    []NodePlugin
	calls     []string
	installed NodePluginInstall
	err       error
}

func (s *nodeClientSpy) record(c string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, c)
}

func (s *nodeClientSpy) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = nil
}

func (s *nodeClientSpy) seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

func (s *nodeClientSpy) PluginsList(context.Context) (*NodePluginsState, error) {
	s.record("list")
	return &NodePluginsState{Available: true, AllowMasterManagement: true, Plugins: s.listed}, nil
}

func (s *nodeClientSpy) PluginsInstall(_ context.Context, req NodePluginInstall) error {
	s.record("install")
	s.installed = req
	return s.err
}

func (s *nodeClientSpy) PluginsSetEnabled(_ context.Context, id string, enabled bool) error {
	s.record("enabled=" + strconv.FormatBool(enabled) + " " + id)
	return s.err
}

func (s *nodeClientSpy) PluginsUninstall(_ context.Context, id string, removeData bool) error {
	s.record("uninstall data=" + strconv.FormatBool(removeData) + " " + id)
	return s.err
}

func (s *nodeClientSpy) PluginsRestart(_ context.Context, id string) error {
	s.record("restart " + id)
	return s.err
}

func (s *nodeClientSpy) PluginsLogs(_ context.Context, id string, lines int) ([]string, error) {
	s.record("logs " + id + " " + strconv.Itoa(lines))
	return []string{"hello"}, s.err
}

func newPluginNodesHarness(t *testing.T, scope string) (*pluginsHarness, *nodeClientSpy, int) {
	t.Helper()
	book := &nodeBook{}
	node := book.add("edge", "online", true)
	spy := &nodeClientSpy{}
	entry := plugins.Entry{
		ID: "blockads", Version: "1.0.0", Type: plugins.TypeProcess, Scope: plugins.ScopeBoth,
		Source: plugins.Source{Owner: "acme", Repo: "blockads"}, Commit: pluginsTestCommit,
		ManifestSHA256: strings.Repeat("ab", 32), Permissions: []string{"network"},
	}
	mgr := newRealManager(t)
	if err := mgr.SetAllowMasterManagement(true); err != nil { // the opt-in is on: only the route list keeps a master out
		t.Fatal(err)
	}
	svc := &PluginService{
		Provider: func() (*plugins.Manager, error) { return mgr, nil },
		Nodes: NewNodePluginsServiceFor(func() ([]plugins.Entry, error) { return []plugins.Entry{entry}, nil },
			book.list, func(NodeInfo) NodePluginsClient { return spy }),
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	api := engine.Group("/panel/api")
	api.Use(func(c *gin.Context) {
		if scope != "" {
			c.Request.Header.Set(headerScope, scope)
		}
		c.Next()
	})
	NewPluginsController(api.Group("/plugins"), svc)
	return &pluginsHarness{engine: engine}, spy, node.ID
}

func TestNodePluginRoutesDriveTheNodeService(t *testing.T) {
	h, spy, id := newPluginNodesHarness(t, "")
	base := "/plugins/nodes/" + strconv.Itoa(id)

	r := h.callAt(t, http.MethodGet, "/plugins/nodes", nil)
	var statuses []NodePluginStatus
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
		spy.reset()
		if r := h.callAt(t, st.method, st.path, st.body); !r.Success {
			t.Fatalf("%s %s: %s", st.method, st.path, r.Msg)
		}
		if calls := spy.seen(); len(calls) == 0 || calls[0] != st.want {
			t.Errorf("%s %s sent %v, want %q first", st.method, st.path, calls, st.want)
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
	h, spy, id := newPluginNodesHarness(t, scopeNodeSync)
	base := "/plugins/nodes/" + strconv.Itoa(id)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/plugins/nodes"},
		{http.MethodPost, base + "/install"},
		{http.MethodPost, base + "/enable/blockads"},
		{http.MethodPost, base + "/update/blockads"},
		{http.MethodGet, base + "/logs/blockads"},
	} {
		if r := h.callAt(t, c.method, c.path, map[string]any{"id": "blockads", "permissions": []string{"network"}}); r.Success {
			t.Errorf("%s %s must not be reachable by a master panel", c.method, c.path)
		}
	}
	if calls := spy.seen(); len(calls) != 0 {
		t.Errorf("node clients were used on behalf of a node: %v", calls)
	}
}

func TestNodeUpdateRouteReinstallsThePinnedCommit(t *testing.T) {
	h, spy, id := newPluginNodesHarness(t, "")
	spy.listed = []NodePlugin{{ID: "blockads", Commit: strings.Repeat("f", 40), ManagedByMaster: true, Enabled: true}}
	base := "/plugins/nodes/" + strconv.Itoa(id)

	if r := h.callAt(t, http.MethodPost, base+"/update/blockads", map[string]any{"permissions": []string{"network", "api:nodes:write"}}); r.Success || !strings.Contains(r.Msg, "differ") {
		t.Errorf("update with other permissions = %+v", r)
	}
	if got := strings.Join(spy.seen(), ","); strings.Contains(got, "uninstall") {
		t.Fatalf("the node was touched by a refused update: %s", got)
	}

	spy.reset()
	if r := h.callAt(t, http.MethodPost, base+"/update/blockads", map[string]any{"permissions": []string{"network"}}); !r.Success {
		t.Fatalf("update: %s", r.Msg)
	}
	want := []string{"list", "uninstall data=false blockads", "install", "enabled=true blockads"}
	if got := spy.seen()[:len(want)]; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("node calls = %v, want %v first", spy.seen(), want)
	}
	if spy.installed.Commit != pluginsTestCommit {
		t.Errorf("installed %+v, want the master's commit", spy.installed)
	}
	if r := h.callAt(t, http.MethodPost, base+"/update/blockads", "not json"); r.Success {
		t.Error("a malformed body was accepted")
	}
}

func TestWithoutALinkToTheNodesTheNodeRoutesSaySo(t *testing.T) {
	h := systemHarness(t, "", nil, nil) // a service with no Nodes
	if r := h.callAt(t, http.MethodGet, "/plugins/nodes", nil); r.Success || !strings.Contains(r.Msg, "not available") {
		t.Errorf("= %+v, want it to say node management is not available", r)
	}
}
