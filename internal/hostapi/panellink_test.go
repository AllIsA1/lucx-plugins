// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// fakeMasterPanel is the master panel's internal socket: the node list and the relay.
type fakeMasterPanel struct {
	mu       sync.Mutex
	nodes    string // the body of GET /panel/api/nodes/list
	relays   []relayCall
	grants   []string
	answer   func(w http.ResponseWriter, call relayCall)
	listFail bool
}

type relayCall struct {
	NodeID string
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Body   json.RawMessage `json:"body"`
}

func (p *fakeMasterPanel) calls() []relayCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.relays)
}

func newFakePanel(t *testing.T) (*fakeMasterPanel, *PanelLink) {
	t.Helper()
	p := &fakeMasterPanel{nodes: `{"success":true,"msg":"","obj":[]}`}
	sock := filepath.Join(t.TempDir(), "p.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.grants = append(p.grants, r.Header.Get("X-LucX-Allowed-Routes"))
		p.mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/panel/api/nodes/list":
			if p.listFail {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			_, _ = io.WriteString(w, p.nodes)
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/panel/api/lucx/plugin-relay/"):
			var call relayCall
			_ = json.NewDecoder(r.Body).Decode(&call)
			call.NodeID = strings.TrimPrefix(r.URL.Path, "/panel/api/lucx/plugin-relay/")
			p.mu.Lock()
			p.relays = append(p.relays, call)
			p.mu.Unlock()
			p.answer(w, call)
		default:
			http.NotFound(w, r)
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return p, NewPanelLink(sock)
}

func relayed(w http.ResponseWriter, status int, body string) {
	w.Header().Set("X-LucX-Relayed", "node")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func TestPanelLinkReadsTheNodesAndSkipsTheOnesItCannotManage(t *testing.T) {
	p, link := newFakePanel(t)
	p.nodes = `{"success":true,"msg":"","obj":[
		{"id":2,"name":"edge","enable":true,"status":"online","nodeType":"lucx","nodeFeatures":["awg","plugins"]},
		{"id":3,"name":"old","enable":true,"status":"offline","nodeType":"vanilla","nodeFeatures":[]},
		{"id":0,"name":"behind-edge","enable":true,"status":"online","transitive":true,"nodeFeatures":["plugins"]}
	]}`
	nodes, err := link.Nodes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 || nodes[0].ID != 2 || nodes[0].Name != "edge" || !nodes[0].Enable || nodes[0].Status != "online" || !slices.Contains(nodes[0].Features, "plugins") {
		t.Errorf("nodes = %+v, want the two direct nodes (a node reached through another is not ours to manage)", nodes)
	}
	if got := p.grants[0]; got != "GET /nodes/list" {
		t.Errorf("grant = %q, want only the node list", got)
	}

	p.listFail = true
	if _, err := link.Nodes(context.Background()); err == nil {
		t.Error("a failing panel was not reported")
	}
}

func TestNodeClientSpeaksTheMastersRoutesThroughTheRelay(t *testing.T) {
	p, link := newFakePanel(t)
	p.answer = func(w http.ResponseWriter, c relayCall) {
		switch {
		case c.Path == "/panel/api/plugins/list":
			relayed(w, 200, `{"success":true,"msg":"","obj":{"available":true,"allowMasterManagement":true,"plugins":[{"id":"hello","commit":"abc","managedByMaster":true,"enabled":true}]}}`)
		case strings.HasPrefix(c.Path, "/panel/api/plugins/logs/"):
			relayed(w, 200, `{"success":true,"msg":"","obj":["one","two"]}`)
		default:
			relayed(w, 200, `{"success":true,"msg":"","obj":null}`)
		}
	}
	c := link.NodeClient(NodeInfo{ID: 7})
	ctx := context.Background()

	st, err := c.PluginsList(ctx)
	if err != nil || !st.AllowMasterManagement || len(st.Plugins) != 1 || !st.Plugins[0].ManagedByMaster {
		t.Fatalf("PluginsList = %+v, %v", st, err)
	}
	if err := c.PluginsInstall(ctx, NodePluginInstall{URL: "https://github.com/a/b", Commit: "c", ManifestSHA256: "m", Permissions: []string{"network"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.PluginsSetEnabled(ctx, "hello", true); err != nil {
		t.Fatal(err)
	}
	if err := c.PluginsSetEnabled(ctx, "hello", false); err != nil {
		t.Fatal(err)
	}
	if err := c.PluginsRestart(ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	if err := c.PluginsUninstall(ctx, "hello", true); err != nil {
		t.Fatal(err)
	}
	lines, err := c.PluginsLogs(ctx, "hello", 50)
	if err != nil || !slices.Equal(lines, []string{"one", "two"}) {
		t.Fatalf("PluginsLogs = %v, %v", lines, err)
	}

	var got []string
	for _, call := range p.calls() {
		if call.NodeID != "7" {
			t.Errorf("relayed to node %s, want 7", call.NodeID)
		}
		got = append(got, call.Method+" "+call.Path)
	}
	want := []string{
		"GET /panel/api/plugins/list", "POST /panel/api/plugins/remote/install", "POST /panel/api/plugins/enable/hello",
		"POST /panel/api/plugins/disable/hello", "POST /panel/api/plugins/restart/hello", "POST /panel/api/plugins/uninstall/hello",
		"GET /panel/api/plugins/logs/hello?lines=50",
	}
	if !slices.Equal(got, want) {
		t.Errorf("the node was asked:\n%v\nwant:\n%v", got, want)
	}
	if !strings.Contains(string(p.calls()[1].Body), `"commit":"c"`) || !strings.Contains(string(p.calls()[5].Body), `"removeData":true`) {
		t.Errorf("bodies were not carried: %s / %s", p.calls()[1].Body, p.calls()[5].Body)
	}
	for _, g := range p.grants {
		if g != "POST /lucx/plugin-relay/:nodeId" {
			t.Errorf("grant = %q, want only the relay route", g)
		}
	}
}

func TestNodeRefusalsKeepTheirStatusAndWordsButThePanelsOwnFailuresDoNot(t *testing.T) {
	p, link := newFakePanel(t)
	c := link.NodeClient(NodeInfo{ID: 1})
	ctx := context.Background()

	p.answer = func(w http.ResponseWriter, _ relayCall) {
		relayed(w, http.StatusForbidden, `{"success":false,"msg":"plugin management by the master is disabled on this node","obj":null}`)
	}
	_, err := c.PluginsList(ctx)
	var refusal *NodeRefusal
	if !errors.As(err, &refusal) || refusal.Status != http.StatusForbidden || refusal.Msg != "plugin management by the master is disabled on this node" {
		t.Errorf("a node's refusal = %v, want a NodeRefusal with its status and words", err)
	}

	p.answer = func(w http.ResponseWriter, _ relayCall) {
		relayed(w, http.StatusOK, `{"success":false,"msg":"plugin not found","obj":null}`)
	}
	err = c.PluginsRestart(ctx, "nope")
	if !errors.As(err, &refusal) || refusal.Msg != "plugin not found" {
		t.Errorf("a failure inside a 200 = %v", err)
	}

	// The same 403 without the relay mark is the panel refusing, not the node.
	p.answer = func(w http.ResponseWriter, _ relayCall) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"success":false,"msg":"this plugin may not call this route"}`)
	}
	err = c.PluginsRestart(ctx, "hello")
	if err == nil || errors.As(err, &refusal) || !strings.Contains(err.Error(), "this plugin may not call this route") {
		t.Errorf("the panel's own refusal = %v, want a plain error that does not look like a locked node", err)
	}

	p.answer = func(w http.ResponseWriter, _ relayCall) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"success":false,"msg":"the node did not answer: dial tcp: refused"}`)
	}
	if err := c.PluginsRestart(ctx, "hello"); err == nil || errors.As(err, &refusal) || !strings.Contains(err.Error(), "did not answer") {
		t.Errorf("an unreachable node = %v", err)
	}
}

func TestNodeClientAgainstADeadPanelFailsCleanly(t *testing.T) {
	link := NewPanelLink(filepath.Join(t.TempDir(), "nothing.sock"))
	if _, err := link.Nodes(context.Background()); err == nil {
		t.Error("nodes from a dead panel")
	}
	if _, err := link.NodeClient(NodeInfo{ID: 1}).PluginsList(context.Background()); err == nil {
		t.Error("a list from a dead panel")
	}
}
