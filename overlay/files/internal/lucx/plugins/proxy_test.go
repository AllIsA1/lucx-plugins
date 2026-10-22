// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type seenRequest struct {
	Method, Path, Query, Body string
	Header                    http.Header
}

// proxyEnv runs a started plugin whose socket is served by handler.
type proxyEnv struct {
	*supEnv
	mu   sync.Mutex
	seen []seenRequest
}

func newProxyEnv(t *testing.T, handler func(w http.ResponseWriter, r *http.Request), opts ...func(*SupervisorOptions)) *proxyEnv {
	t.Helper()
	sockDir, err := os.MkdirTemp("", "lucxp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	e := &proxyEnv{supEnv: newSupEnv(t, nil, opts...)}
	e.runner.socketDir = sockDir
	e.enable(t)
	e.reconcile()

	ln, err := net.Listen("unix", filepath.Join(sockDir, "traffic-alerts.sock"))
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		e.mu.Lock()
		e.seen = append(e.seen, seenRequest{r.Method, r.URL.Path, r.URL.RawQuery, string(body), r.Header.Clone()})
		e.mu.Unlock()
		if handler != nil {
			handler(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return e
}

func (e *proxyEnv) last(t *testing.T) seenRequest {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.seen) == 0 {
		t.Fatal("the plugin received no request")
	}
	return e.seen[len(e.seen)-1]
}

func (e *proxyEnv) do(req *http.Request, subpath string) (*httptest.ResponseRecorder, error) {
	w := httptest.NewRecorder()
	err := e.sup.Proxy(w, req, "traffic-alerts", subpath, "admin")
	return w, err
}

func TestProxyForwardsRequestsWithoutLeakingPanelCredentials(t *testing.T) {
	e := newProxyEnv(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/anything?x=1&y=two", strings.NewReader("payload"))
	req.Header.Set("Cookie", "3x-ui=session-secret")
	req.Header.Set("Authorization", "Bearer admin-api-token")
	req.Header.Set("X-CSRF-Token", "csrf-secret")
	req.Header.Set("X-LucX-User", "evil")
	req.Header.Set("X-Lucx-Anything", "spoof")
	req.Header.Set("Content-Type", "application/json")

	w, err := e.do(req, "/hello/world")
	if err != nil {
		t.Fatalf("Proxy: %v", err)
	}
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"ok":true}` {
		t.Fatalf("response = %d %q", w.Code, w.Body.String())
	}
	got := e.last(t)
	if got.Method != http.MethodPost || got.Path != "/hello/world" || got.Query != "x=1&y=two" || got.Body != "payload" {
		t.Errorf("forwarded request = %+v", got)
	}
	for _, h := range []string{"Cookie", "Authorization", "X-Csrf-Token", "X-Lucx-Anything"} {
		if v := got.Header.Get(h); v != "" {
			t.Errorf("the plugin received %s: %q", h, v)
		}
	}
	if got.Header.Get("X-Lucx-User") != "admin" || got.Header.Get("X-Lucx-Plugin-Id") != "traffic-alerts" || got.Header.Get("X-Lucx-Request-Id") == "" {
		t.Errorf("identity headers = %v", got.Header)
	}
	if got.Header.Get("Content-Type") != "application/json" {
		t.Error("ordinary headers must pass through")
	}
}

func TestProxyKeepsPluginResponsesFromActingWithPanelPrivileges(t *testing.T) {
	e := newProxyEnv(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Set-Cookie", "3x-ui=stolen; Path=/")
		w.Header().Set("Content-Security-Policy", "default-src *")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<script>alert(1)</script>"))
	})
	w, err := e.do(httptest.NewRequest(http.MethodGet, "/x", nil), "/page")
	if err != nil {
		t.Fatal(err)
	}
	h := w.Header()
	if h.Get("Set-Cookie") != "" || h.Get("Access-Control-Allow-Origin") != "" || h.Get("Access-Control-Allow-Credentials") != "" {
		t.Errorf("plugin response carries browser-privileged headers: %v", h)
	}
	if got := h.Values("Content-Security-Policy"); len(got) != 1 || got[0] != pageCSP {
		t.Errorf("CSP = %q, want exactly the page policy that the plugin cannot relax", got)
	}
	if h.Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Errorf("X-Frame-Options = %q", h.Get("X-Frame-Options"))
	}
	if h.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}
}

func TestProxyRefusesRequestsThatAreNotForThePluginsPublicAPI(t *testing.T) {
	e := newProxyEnv(t, nil)
	for _, p := range []string{"/_lucx/health", "/_lucx/uninstall", "/_lucx/../_lucx/health", "/a/../_lucx/x", "/_lucx"} {
		if _, err := e.do(httptest.NewRequest(http.MethodGet, "/x", nil), p); !errors.Is(err, ErrReservedPath) {
			t.Errorf("path %q: err = %v, want ErrReservedPath", p, err)
		}
	}
	if len(e.seen) != 0 {
		t.Errorf("a reserved path reached the plugin: %+v", e.seen)
	}

	if _, err := e.do(httptest.NewRequest(http.MethodGet, "/x", nil), "/../../etc/passwd"); err != nil {
		t.Fatal(err)
	}
	if got := e.last(t).Path; got != "/etc/passwd" {
		t.Errorf("dot segments must be resolved inside the plugin's own namespace, got %q", got)
	}

	ws := httptest.NewRequest(http.MethodGet, "/x", nil)
	ws.Header.Set("Connection", "Upgrade")
	ws.Header.Set("Upgrade", "websocket")
	if _, err := e.do(ws, "/ws"); !errors.Is(err, ErrUpgradeUnsupported) {
		t.Errorf("upgrade request: err = %v, want ErrUpgradeUnsupported", err)
	}
}

func TestProxyPreflightErrors(t *testing.T) {
	e := newSupEnv(t, nil)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if err := e.sup.Proxy(w, req, "missing", "/a", "admin"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown plugin: %v", err)
	}
	if err := e.sup.Proxy(w, req, "traffic-alerts", "/a", "admin"); !errors.Is(err, ErrNotRunning) {
		t.Errorf("disabled plugin: %v", err)
	}
	e.enable(t)
	e.reconcile()
	e.runner.crash("traffic-alerts")
	e.runner.units["traffic-alerts"].state = StateStopping
	e.reconcile()
	if err := e.sup.Proxy(w, req, "traffic-alerts", "/a", "admin"); !errors.Is(err, ErrNotRunning) {
		t.Errorf("a plugin that is not running: %v", err)
	}

	ui := newSupEnv(t, func(m map[string]any) {
		m["type"] = "ui"
		delete(m, "run")
		m["permissions"] = []string{"api:clients:read"}
	})
	ui.enable(t)
	if err := ui.sup.Proxy(w, req, "traffic-alerts", "/a", "admin"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a ui plugin has no process to proxy to: %v", err)
	}
}

func TestProxyReportsUnreachableAndOversizedRequests(t *testing.T) {
	down := newSupEnv(t, nil)
	down.runner.socketDir = t.TempDir()
	down.enable(t)
	down.reconcile()
	w := httptest.NewRecorder()
	if err := down.sup.Proxy(w, httptest.NewRequest(http.MethodGet, "/x", nil), "traffic-alerts", "/a", "admin"); err != nil {
		t.Fatalf("a dead socket is answered with 502, not a Go error: %v", err)
	}
	var body map[string]any
	if w.Code != http.StatusBadGateway || json.Unmarshal(w.Body.Bytes(), &body) != nil || body["success"] != false {
		t.Errorf("unreachable plugin: %d %s", w.Code, w.Body.String())
	}

	big := newProxyEnv(t, nil, func(o *SupervisorOptions) { o.MaxProxyBody = 1024 })
	rw, err := big.do(httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(strings.Repeat("a", 4096))), "/upload")
	if err != nil {
		t.Fatal(err)
	}
	if rw.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body: status = %d, want 413", rw.Code)
	}
}
