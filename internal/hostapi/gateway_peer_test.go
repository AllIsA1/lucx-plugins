// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/AllIsA1/lucx-plugins/internal/plugins"
)

// peerGateway installs better-cores and then better-warp, which requires it and holds peer:better-cores, and
// returns better-warp's token.
func peerGateway(t *testing.T, callerPerms string) (*testGateway, string) {
	t.Helper()
	archive := pluginsArchiveWithManifest(t, `{"id":"better-cores","name":"Better Cores","version":"1.0.0","apiVersion":"1","type":"process",`+
		`"run":{"command":"bin/${arch}/alerts"},"permissions":[]}`)
	m := newManagerServingFunc(t, func() []byte { return archive })
	install := func(id string) plugins.Entry {
		p, err := m.Preview(context.Background(), "https://github.com/acme/alerts")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.Install(p.Token, p.Manifest.Permissions); err != nil {
			t.Fatal(err)
		}
		if err := m.SetEnabled(id, true); err != nil {
			t.Fatal(err)
		}
		e, _ := m.Get(id)
		return e
	}
	install("better-cores")
	archive = pluginsArchiveWithManifest(t, `{"id":"better-warp","name":"Better WARP","version":"1.0.0","apiVersion":"1","type":"process",`+
		`"run":{"command":"bin/${arch}/alerts"},"permissions":`+callerPerms+`,"requires":["better-cores"]}`)
	caller := install("better-warp")

	socket, _ := startFakePanel(t)
	svc := &PluginService{Provider: func() (*plugins.Manager, error) { return m, nil }}
	srv := httptest.NewServer(NewGateway(svc, filepath.Join(filepath.Dir(socket), "panel.sock")).Handler())
	t.Cleanup(srv.Close)
	return &testGateway{t: t, url: srv.URL}, m.IssueToken(caller)
}

func TestGatewayPeerCallNeedsAValidTokenAndThePermissionForThatPlugin(t *testing.T) {
	g, token := peerGateway(t, `["peer:better-cores"]`)

	if rec := call(g, http.MethodGet, "/peer/better-cores/warp/check", "", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d, want 401", rec.Code)
	}
	if rec := call(g, http.MethodGet, "/peer/better-cores/warp/check", "lxp.garbage", "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("garbage token: %d, want 401", rec.Code)
	}
	// Holding the permission for one plugin says nothing about another.
	if rec := call(g, http.MethodGet, "/peer/some-other-plugin/warp/check", token, "", nil); rec.Code != http.StatusForbidden {
		t.Errorf("another plugin: %d, want 403", rec.Code)
	}
	// Allowed: the permission is held, and with no process supervisor the peer is simply not running.
	if rec := call(g, http.MethodGet, "/peer/better-cores/warp/check", token, "", nil); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("allowed call: %d %s, want 503 (not running)", rec.Code, rec.Body)
	}
}

func TestGatewayPeerCallIsRefusedForAPluginWithoutTheSpecificPermission(t *testing.T) {
	g, token := peerGateway(t, `["network"]`)
	if rec := call(g, http.MethodGet, "/peer/better-cores/warp/check", token, "", nil); rec.Code != http.StatusForbidden {
		t.Errorf("a plugin without peer:better-cores: %d, want 403", rec.Code)
	}
}
