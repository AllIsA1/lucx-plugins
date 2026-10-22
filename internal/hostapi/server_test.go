// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/AllIsA1/lucx-plugins/internal/plugins"
)

func unixClient(socket string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}
}

func serveOn(t *testing.T, socket string, srv *Server) {
	t.Helper()
	ln, err := Listen(socket)
	if err != nil {
		t.Fatal(err)
	}
	hs := &http.Server{Handler: srv.Handler()}
	go func() { _ = hs.Serve(ln) }()
	t.Cleanup(func() { _ = hs.Close() })
}

func TestListenCreatesAPrivateSocketAndReplacesAStaleOne(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "run", "host.sock")
	if err := os.MkdirAll(filepath.Dir(socket), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(socket, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := Listen(socket)
	if err != nil {
		t.Fatalf("Listen over a stale file: %v", err)
	}
	defer ln.Close()
	if st, err := os.Stat(socket); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("socket mode: %v %v; only the panel (root) may talk to the host", st, err)
	}
}

func TestServerServesThePanelRoutesOnTheUnixSocket(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "host.sock")
	svc := &PluginService{Provider: func() (*plugins.Manager, error) { return nil, plugins.ErrDisabled }}
	serveOn(t, socket, NewServer(svc))
	c := unixClient(socket)

	resp, err := c.Get("http://host/panel/api/plugins/list")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var msg Msg
	if err := json.NewDecoder(resp.Body).Decode(&msg); err != nil || !msg.Success {
		t.Fatalf("GET /panel/api/plugins/list = %+v, %v; the panel's frontend calls exactly this path", msg, err)
	}

	resp2, err := c.Get("http://host/panel/plugin-frame/not-a-token/ui/index.html")
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("a bad page token = %d, want 404", resp2.StatusCode)
	}
	resp3, err := c.Get("http://host/somewhere/else")
	if err != nil {
		t.Fatal(err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusNotFound {
		t.Errorf("an unknown path = %d, want 404", resp3.StatusCode)
	}
}
