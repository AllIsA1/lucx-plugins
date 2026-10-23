// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AllIsA1/lucx-plugins/internal/builds"
	"github.com/AllIsA1/lucx-plugins/internal/hostupdate"
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
	go func() { done <- run(ctx, cfg, newUpdater(cfg)) }()

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

func scriptFor(version string) []byte {
	return []byte("#!/bin/sh\n# " + version + "\necho " + version + "\n")
}

func hexSum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// TestHostReplacesItselfOnRequestAndAsksToBeRestarted runs the whole path in one process: the admin
// route, the signed list, the download, the swap on disk and the graceful stop that precedes the restart.
func TestHostReplacesItselfOnRequestAndAsksToBeRestarted(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XUI_PLUGINS_RUNNER", "process")
	t.Setenv("XUI_PLUGINS_RUNTIME_DIR", filepath.Join(dir, "run"))

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	newBin := scriptFor("host-2")
	doc := &builds.HostIndex{Schema: 1, Sequence: 1}
	if err := doc.Set("host-2", map[string]string{"amd64": hexSum(newBin), "arm64": hexSum(append(newBin, '\n'))}); err != nil {
		t.Fatal(err)
	}
	doc.GeneratedAt = time.Now().UTC()
	raw, _ := builds.MarshalHost(doc)
	sig := builds.SignHost(priv, raw)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/list/host.json":
			_, _ = w.Write(raw)
		case r.URL.Path == "/list/host.json.sig":
			_, _ = w.Write([]byte(sig))
		case strings.HasPrefix(r.URL.Path, "/dl/host-2/lucx-plugin-host-linux-amd64"):
			_, _ = w.Write(newBin)
		case strings.HasPrefix(r.URL.Path, "/dl/host-2/lucx-plugin-host-linux-arm64"):
			_, _ = w.Write(append(newBin, '\n'))
		default:
			http.NotFound(w, r)
		}
	}))
	defer source.Close()

	exe := filepath.Join(dir, "lucx-plugin-host")
	if err := os.WriteFile(exe, scriptFor("host-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config{
		StateDir: filepath.Join(dir, "state"), Socket: filepath.Join(dir, "host.sock"), Gateway: freeAddr(t),
		PanelSocket: filepath.Join(dir, "panel.sock"), Version: "host-1",
		Exe: exe, UpdateBase: source.URL + "/list", UpdateKeys: []ed25519.PublicKey{pub}, DownloadBase: source.URL + "/dl",
	}
	done := make(chan error, 1)
	go func() { done <- run(context.Background(), cfg, newUpdater(cfg)) }()
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", cfg.Socket)
	}}, Timeout: 10 * time.Second}
	call := func(method, path string) (bool, string, json.RawMessage) {
		req, _ := http.NewRequest(method, "http://host"+path, nil)
		resp, err := client.Do(req)
		if err != nil {
			return false, err.Error(), nil
		}
		defer resp.Body.Close()
		var m struct {
			Success bool            `json:"success"`
			Msg     string          `json:"msg"`
			Obj     json.RawMessage `json:"obj"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&m)
		return m.Success, m.Msg, m.Obj
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if ok, _, _ := call(http.MethodGet, "/panel/api/plugins/system"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the host did not come up")
		}
		time.Sleep(20 * time.Millisecond)
	}

	if ok, msg, obj := call(http.MethodPost, "/panel/api/plugins/system/check"); !ok {
		t.Fatalf("check: %s", msg)
	} else {
		_ = obj
	}
	_, _, obj := call(http.MethodGet, "/panel/api/plugins/system")
	var st hostupdate.Status
	if err := json.Unmarshal(obj, &st); err != nil || !st.Managed || st.Version != "host-1" || st.Available != "host-2" {
		t.Fatalf("status after the check = %+v (%v)", st, err)
	}
	if ok, msg, _ := call(http.MethodPost, "/panel/api/plugins/system/update"); !ok {
		t.Fatalf("update: %s", msg)
	}
	select {
	case err := <-done:
		if !errors.Is(err, errRestart) {
			t.Errorf("run returned %v, want it to ask for a restart", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the host did not stop for the restart")
	}
	if got, _ := os.ReadFile(exe); string(got) != string(newBin) {
		t.Errorf("binary on disk = %q, want the new release", got)
	}
	if prev, _ := os.ReadFile(exe + ".prev"); string(prev) != string(scriptFor("host-1")) {
		t.Errorf("previous binary = %q", prev)
	}
}

func TestADevelopmentBuildDoesNotUpdateItself(t *testing.T) {
	cfg := config{StateDir: t.TempDir(), Version: "dev", Exe: "/nowhere/lucx-plugin-host"}
	if st := newUpdater(cfg).Status(); st.Managed {
		t.Errorf("a dev build is managed: %+v", st)
	}
	cfg.Version = "host-4"
	if st := newUpdater(cfg).Status(); !st.Managed {
		t.Errorf("a release is not managed: %+v", st)
	}
	cfg.Exe = ""
	if st := newUpdater(cfg).Status(); st.Managed {
		t.Errorf("a host that does not know its own file is managed: %+v", st)
	}
}

// TestHostListsTheMastersNodesThroughThePanelsSocket runs the wiring: the host asks the panel's
// internal socket for the nodes, polls each reachable one through the relay and reports what it read.
func TestHostListsTheMastersNodesThroughThePanelsSocket(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XUI_PLUGINS_RUNNER", "process")
	t.Setenv("XUI_PLUGINS_RUNTIME_DIR", filepath.Join(dir, "run"))
	panelSock := filepath.Join(dir, "panel.sock")
	ln, err := net.Listen("unix", panelSock)
	if err != nil {
		t.Fatal(err)
	}
	var grant string
	panel := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		grant = r.Header.Get("X-LucX-Allowed-Routes")
		switch r.URL.Path {
		case "/panel/api/nodes/list":
			_, _ = w.Write([]byte(`{"success":true,"msg":"","obj":[{"id":4,"name":"edge","enable":true,"status":"online","nodeFeatures":["plugins"]}]}`))
		case "/panel/api/lucx/plugin-relay/4":
			w.Header().Set("X-LucX-Relayed", "node")
			_, _ = w.Write([]byte(`{"success":true,"msg":"","obj":{"available":true,"allowMasterManagement":true,"plugins":[]}}`))
		default:
			http.NotFound(w, r)
		}
	})}
	go func() { _ = panel.Serve(ln) }()
	defer panel.Close()

	cfg := config{StateDir: filepath.Join(dir, "state"), Socket: filepath.Join(dir, "host.sock"), Gateway: freeAddr(t), PanelSocket: panelSock, Version: "test"}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, cfg, newUpdater(cfg)) }()
	defer func() { cancel(); <-done }()
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", cfg.Socket)
	}}}
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := client.Get("http://host/panel/api/plugins/nodes")
		if err == nil {
			var m struct {
				Success bool `json:"success"`
				Obj     []struct {
					NodeName string `json:"nodeName"`
					State    string `json:"state"`
				} `json:"obj"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&m)
			resp.Body.Close()
			if m.Success && len(m.Obj) == 1 && m.Obj[0].State == "ok" {
				if m.Obj[0].NodeName != "edge" {
					t.Errorf("nodes = %+v", m.Obj)
				}
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the nodes were never read through the relay (%v)", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if grant != "GET /nodes/list" && grant != "POST /lucx/plugin-relay/:nodeId" {
		t.Errorf("the host asked the panel with grant %q", grant)
	}
}
