// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func TestHostServesItsSocketsAndStopsCleanly(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XUI_PLUGINS_RUNNER", "process") // never touch this machine's systemd from a test
	t.Setenv("XUI_PLUGINS_RUNTIME_DIR", filepath.Join(dir, "run"))
	cfg := config{
		StateDir:    filepath.Join(dir, "state"),
		Socket:      filepath.Join(dir, "host.sock"),
		Gateway:     freeAddr(t),
		PanelSocket: filepath.Join(dir, "panel.sock"),
		Version:     "test",
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, cfg) }()

	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", cfg.Socket)
	}}, Timeout: 3 * time.Second}
	var hello struct {
		IntegrationAPI int    `json:"integrationApi"`
		Host           string `json:"host"`
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := client.Get("http://host/internal/hello")
		if err == nil {
			err = json.NewDecoder(resp.Body).Decode(&hello)
			resp.Body.Close()
			if err == nil {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the host did not come up: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if hello.IntegrationAPI != 1 || hello.Host != "test" {
		t.Errorf("hello = %+v", hello)
	}
	resp, err := client.Get("http://host/panel/api/plugins/list")
	if err != nil {
		t.Fatal(err)
	}
	var msg struct {
		Success bool `json:"success"`
		Obj     struct {
			Available bool `json:"available"`
		} `json:"obj"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&msg); err != nil || !msg.Success || !msg.Obj.Available {
		t.Errorf("the plugin manager did not come up with the host: %+v, %v", msg, err)
	}
	resp.Body.Close()

	gw, err := http.Get("http://" + cfg.Gateway + "/panel/api/clients/list")
	if err != nil {
		t.Fatalf("the gateway does not listen on %s: %v", cfg.Gateway, err)
	}
	gw.Body.Close()
	if gw.StatusCode != http.StatusUnauthorized {
		t.Errorf("the gateway without a token = %d, want 401", gw.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the host did not stop")
	}
}
