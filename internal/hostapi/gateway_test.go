// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostapi

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/AllIsA1/lucx-plugins/internal/plugins"
)

// fakePanel plays the panel's internal API socket and records what reaches it.
type fakePanel struct {
	mu   sync.Mutex
	seen []*http.Request
	body [][]byte
}

func startFakePanel(t *testing.T) (socket string, panel *fakePanel) {
	t.Helper()
	socket = filepath.Join(t.TempDir(), "panel.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	panel = &fakePanel{}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		panel.mu.Lock()
		panel.seen = append(panel.seen, r)
		panel.body = append(panel.body, raw)
		panel.mu.Unlock()
		w.Header().Set("Set-Cookie", "session=stolen")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"success":true,"obj":[1,2]}`))
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return socket, panel
}

func (p *fakePanel) requests() []*http.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*http.Request(nil), p.seen...)
}

// gatewayWithPlugin installs traffic-alerts, with the default permissions (api:clients:read and
// network) or with the given manifest permissions, and returns the token it would be issued.
func gatewayWithPlugin(t *testing.T, perms ...string) (*testGateway, *fakePanel, string) {
	t.Helper()
	archive := pluginsTestArchive(t)
	if perms != nil {
		list, _ := json.Marshal(perms)
		archive = pluginsArchiveWithManifest(t, `{"id":"traffic-alerts","name":"Traffic Alerts","version":"1.2.0","apiVersion":"1","type":"process",`+
			`"run":{"command":"bin/${arch}/alerts"},"permissions":`+string(list)+`}`)
	}
	m := newManagerServing(t, archive)
	p, err := m.Preview(context.Background(), "https://github.com/acme/alerts")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Install(p.Token, p.Manifest.Permissions); err != nil {
		t.Fatal(err)
	}
	if err := m.SetEnabled("traffic-alerts", true); err != nil { // a token only works for an enabled plugin
		t.Fatal(err)
	}
	e, _ := m.Get("traffic-alerts")
	socket, panel := startFakePanel(t)
	svc := &PluginService{Provider: func() (*plugins.Manager, error) { return m, nil }}
	srv := httptest.NewServer(NewGateway(svc, socket).Handler()) // a real server: the reverse proxy needs a real connection
	t.Cleanup(srv.Close)
	return &testGateway{t: t, url: srv.URL}, panel, m.IssueToken(e)
}

type testGateway struct {
	t   *testing.T
	url string
}

type gatewayReply struct {
	Code   int
	Body   string
	Header http.Header
}

func call(g *testGateway, method, path, token string, body string, hdr map[string]string) gatewayReply {
	req, err := http.NewRequest(method, g.url+path, strings.NewReader(body))
	if err != nil {
		g.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		g.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return gatewayReply{Code: resp.StatusCode, Body: string(raw), Header: resp.Header}
}

func TestGatewayRefusesCallsWithoutAValidPluginToken(t *testing.T) {
	g, panel, token := gatewayWithPlugin(t)
	for name, tok := range map[string]string{"none": "", "garbage": "lxp.garbage", "a panel api token": "admin-token-value", "tampered": token + "x"} {
		if rec := call(g, http.MethodGet, "/panel/api/clients/list", tok, "", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, rec.Code)
		}
	}
	if n := len(panel.requests()); n != 0 {
		t.Errorf("%d unauthenticated calls reached the panel", n)
	}
}

func TestGatewayForwardsAllowedCallsAndTellsThePanelWhatThePluginMayCall(t *testing.T) {
	g, panel, token := gatewayWithPlugin(t)
	rec := call(g, http.MethodGet, "/panel/api/clients/list?page=2", token, "", map[string]string{
		"Cookie":                "session=browser",
		"X-Csrf-Token":          "csrf",
		"X-LucX-Allowed-Routes": "POST /clients/add", // a plugin must not be able to widen its own list
		"X-LucX-Plugin-Id":      "someone-else",
	})
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body, `"obj":[1,2]`) {
		t.Fatalf("the panel's answer was not relayed: %d %s", rec.Code, rec.Body)
	}
	if rec.Header.Get("Set-Cookie") != "" {
		t.Error("the panel's Set-Cookie reached the plugin")
	}
	seen := panel.requests()
	if len(seen) != 1 {
		t.Fatalf("panel saw %d requests", len(seen))
	}
	r := seen[0]
	if r.URL.Path != "/panel/api/clients/list" || r.URL.RawQuery != "page=2" || r.Method != http.MethodGet {
		t.Errorf("forwarded %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
	}
	if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("X-Csrf-Token") != "" {
		t.Errorf("credentials of the plugin's request travelled on: %v", r.Header)
	}
	if got := r.Header.Get("X-LucX-Plugin-Id"); got != "traffic-alerts" {
		t.Errorf("X-LucX-Plugin-Id = %q", got)
	}
	allowed := r.Header.Get("X-LucX-Allowed-Routes")
	if !strings.Contains(allowed, "GET /clients/list") || strings.Contains(allowed, "POST /clients/add") {
		t.Errorf("allowed routes = %q: the plugin's own list, never the one it sent", allowed)
	}
}

func TestGatewayRefusesAPluginWithNoAPIPermissions(t *testing.T) {
	g, panel, token := gatewayWithPlugin(t, "network")
	if rec := call(g, http.MethodGet, "/panel/api/clients/list", token, "", nil); rec.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403", rec.Code)
	}
	if n := len(panel.requests()); n != 0 {
		t.Errorf("the panel saw %d calls from a plugin that holds no API permission", n)
	}
}

func TestGatewayPassesBodiesButBoundsThem(t *testing.T) {
	g, panel, token := gatewayWithPlugin(t)
	if rec := call(g, http.MethodPost, "/panel/api/clients/list", token, `{"a":1}`, map[string]string{"Content-Type": "application/json"}); rec.Code != http.StatusAccepted {
		t.Fatalf("status %d", rec.Code)
	}
	if got := string(panel.body[0]); got != `{"a":1}` {
		t.Errorf("body = %q", got)
	}
	if rec := call(g, http.MethodPost, "/panel/api/clients/list", token, strings.Repeat("x", maxGatewayBody+1), nil); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("an oversized body = %d, want 413", rec.Code)
	}
}

func TestGatewayOnlyServesThePanelAPI(t *testing.T) {
	g, panel, token := gatewayWithPlugin(t)
	for _, path := range []string{"/", "/login", "/panel/settings", "/panel/api", "/panel/apix/clients/list"} {
		if rec := call(g, http.MethodGet, path, token, "", nil); rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, rec.Code)
		}
	}
	if n := len(panel.requests()); n != 0 {
		t.Errorf("%d calls outside /panel/api reached the panel", n)
	}
}
