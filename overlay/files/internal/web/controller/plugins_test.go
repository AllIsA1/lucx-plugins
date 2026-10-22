// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package controller

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/lucx/plugins"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

const pluginsTestCommit = "0123456789abcdef0123456789abcdef01234567"

func pluginsTestArchive(t *testing.T) []byte {
	t.Helper()
	return pluginsArchiveWithManifest(t, `{"id":"traffic-alerts","name":"Traffic Alerts","version":"1.2.0","apiVersion":"1","type":"process",`+
		`"run":{"command":"bin/${arch}/alerts"},"permissions":["api:clients:read","network"]}`)
}

func pluginsArchiveWithManifest(t *testing.T, manifest string) []byte {
	t.Helper()
	return pluginsArchiveAt(t, manifest, pluginsTestCommit)
}

func pluginsArchiveAt(t *testing.T, manifest, commit string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	global := &tar.Header{Typeflag: tar.TypeXGlobalHeader, Name: "pax_global_header", PAXRecords: map[string]string{"comment": commit}}
	if err := tw.WriteHeader(global); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"alerts-0123456/lucx-plugin.json": manifest,
		"alerts-0123456/bin/amd64/alerts": "ELF",
	} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type pluginsHarness struct {
	engine *gin.Engine
}

func newPluginsHarness(t *testing.T, provider func() (*plugins.Manager, error)) *pluginsHarness {
	t.Helper()
	return newPluginsHarnessWith(t, &service.PluginService{Provider: provider})
}

func newPluginsHarnessWith(t *testing.T, svc *service.PluginService) *pluginsHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(sessions.Sessions("3x-ui", cookie.NewStore([]byte("plugins-test-secret"))))
	NewPluginsController(engine.Group("/plugins"), svc)
	return &pluginsHarness{engine: engine}
}

func newRealManager(t *testing.T) *plugins.Manager {
	t.Helper()
	return newManagerServing(t, pluginsTestArchive(t))
}

func newManagerServing(t *testing.T, archive []byte) *plugins.Manager {
	t.Helper()
	return newManagerServingFunc(t, func() []byte { return archive })
}

// newManagerServingFunc serves whatever archive the function returns at the moment of the download.
func newManagerServingFunc(t *testing.T, archive func() []byte, opts ...func(*plugins.Config)) *plugins.Manager {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/acme/alerts/tar.gz/") {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(archive())
	}))
	t.Cleanup(srv.Close)
	root := t.TempDir()
	cfg := plugins.Config{
		CodeDir:      filepath.Join(root, "plugins"),
		DataDir:      filepath.Join(root, "data"),
		RegistryPath: filepath.Join(root, "plugins.json"),
		ArchiveBase:  srv.URL,
		HTTPClient:   srv.Client(),
		PanelVersion: "3.10.2",
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	m, err := plugins.NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

type pluginsResp struct {
	Success bool            `json:"success"`
	Msg     string          `json:"msg"`
	Obj     json.RawMessage `json:"obj"`
}

func (h *pluginsHarness) call(t *testing.T, method, path string, body any) pluginsResp {
	t.Helper()
	var rdr *bytes.Reader
	if s, ok := body.(string); ok {
		rdr = bytes.NewReader([]byte(s))
	} else if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(raw)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK && rec.Code != http.StatusForbidden {
		t.Fatalf("%s %s: HTTP %d: %s", method, path, rec.Code, rec.Body.String())
	}
	var out pluginsResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	if out.Success && out.Msg != "" {
		t.Errorf("%s %s: a successful call carries msg %q, which the UI would show as a toast", method, path, out.Msg)
	}
	return out
}

type stateView struct {
	Available             bool               `json:"available"`
	AllowMasterManagement bool               `json:"allowMasterManagement"`
	ProcessesAvailable    bool               `json:"processesAvailable"`
	ProcessesError        string             `json:"processesError"`
	SafeMode              bool               `json:"safeMode"`
	Error                 string             `json:"error"`
	ArchiveBase           string             `json:"archiveBase"`
	CustomArchiveBase     string             `json:"customArchiveBase"`
	DefaultArchiveBase    string             `json:"defaultArchiveBase"`
	Plugins               []pluginView       `json:"plugins"`
	ProcessesIsolation    *plugins.Isolation `json:"processesIsolation"`
}

type pluginView struct {
	plugins.Entry
	Runtime *plugins.PluginRuntime `json:"runtime"`
}

func (h *pluginsHarness) state(t *testing.T) stateView {
	t.Helper()
	r := h.call(t, http.MethodGet, "/plugins/list", nil)
	if !r.Success {
		t.Fatalf("list failed: %s", r.Msg)
	}
	var s stateView
	if err := json.Unmarshal(r.Obj, &s); err != nil {
		t.Fatalf("decode state %s: %v", r.Obj, err)
	}
	return s
}

func TestPluginsListWhenManagerUnavailable(t *testing.T) {
	t.Setenv("XUI_PLUGINS_DISABLED", "1")
	h := newPluginsHarness(t, func() (*plugins.Manager, error) { return nil, plugins.ErrDisabled })
	s := h.state(t)
	if s.Available || !s.SafeMode || !strings.Contains(s.Error, "XUI_PLUGINS_DISABLED") {
		t.Errorf("state = %+v", s)
	}
	if s.Plugins == nil || len(s.Plugins) != 0 {
		t.Errorf("Plugins = %#v, want an empty non-nil list", s.Plugins)
	}
	if s.DefaultArchiveBase != plugins.DefaultArchiveBase {
		t.Errorf("DefaultArchiveBase = %q", s.DefaultArchiveBase)
	}
	for _, ep := range []string{"/plugins/preview", "/plugins/install", "/plugins/enable/traffic-alerts", "/plugins/uninstall/traffic-alerts", "/plugins/settings"} {
		if r := h.call(t, http.MethodPost, ep, map[string]any{"url": "https://github.com/acme/alerts"}); r.Success {
			t.Errorf("POST %s succeeded while plugins are disabled", ep)
		}
	}
}

func TestPluginsInstallFlow(t *testing.T) {
	m := newRealManager(t)
	h := newPluginsHarness(t, func() (*plugins.Manager, error) { return m, nil })

	preview := h.call(t, http.MethodPost, "/plugins/preview", map[string]string{"url": "https://github.com/acme/alerts"})
	if !preview.Success {
		t.Fatalf("preview failed: %s", preview.Msg)
	}
	var p plugins.Preview
	if err := json.Unmarshal(preview.Obj, &p); err != nil {
		t.Fatal(err)
	}
	if p.Token == "" || p.Manifest.ID != "traffic-alerts" || len(p.Dangerous) != 1 || p.Dangerous[0] != "network" {
		t.Fatalf("preview = %+v", p)
	}

	wrong := h.call(t, http.MethodPost, "/plugins/install", map[string]any{"token": p.Token, "permissions": []string{"api:clients:read"}})
	if wrong.Success {
		t.Fatal("install with incomplete permission confirmation succeeded")
	}
	if got := h.state(t).Plugins; len(got) != 0 {
		t.Fatalf("plugin registered after a rejected confirmation: %+v", got)
	}

	p2 := previewOK(t, h)
	ok := h.call(t, http.MethodPost, "/plugins/install", map[string]any{"token": p2.Token, "permissions": p2.Manifest.Permissions})
	if !ok.Success {
		t.Fatalf("install failed: %s", ok.Msg)
	}
	list := h.state(t).Plugins
	if len(list) != 1 || list[0].ID != "traffic-alerts" || list[0].Enabled || list[0].Commit != pluginsTestCommit {
		t.Fatalf("list after install = %+v", list)
	}

	if r := h.call(t, http.MethodPost, "/plugins/enable/traffic-alerts", nil); !r.Success {
		t.Fatalf("enable: %s", r.Msg)
	}
	if got := h.state(t).Plugins; !got[0].Enabled {
		t.Error("plugin not enabled")
	}
	if r := h.call(t, http.MethodPost, "/plugins/disable/traffic-alerts", nil); !r.Success {
		t.Fatalf("disable: %s", r.Msg)
	}
	if got := h.state(t).Plugins; got[0].Enabled {
		t.Error("plugin still enabled")
	}

	if r := h.call(t, http.MethodPost, "/plugins/uninstall/traffic-alerts", map[string]bool{"removeData": true}); !r.Success {
		t.Fatalf("uninstall: %s", r.Msg)
	}
	if got := h.state(t).Plugins; len(got) != 0 {
		t.Errorf("list after uninstall = %+v", got)
	}
	if r := h.call(t, http.MethodPost, "/plugins/enable/traffic-alerts", nil); r.Success {
		t.Error("enabling an uninstalled plugin succeeded")
	}
}

func previewOK(t *testing.T, h *pluginsHarness) plugins.Preview {
	t.Helper()
	r := h.call(t, http.MethodPost, "/plugins/preview", map[string]string{"url": "https://github.com/acme/alerts"})
	if !r.Success {
		t.Fatalf("preview failed: %s", r.Msg)
	}
	var p plugins.Preview
	if err := json.Unmarshal(r.Obj, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPluginsPreviewRejectsForeignHost(t *testing.T) {
	m := newRealManager(t)
	h := newPluginsHarness(t, func() (*plugins.Manager, error) { return m, nil })
	r := h.call(t, http.MethodPost, "/plugins/preview", map[string]string{"url": "https://evil.example/acme/alerts"})
	if r.Success {
		t.Fatal("preview of a non-GitHub URL succeeded")
	}
}

func TestPluginsRejectMalformedBodies(t *testing.T) {
	m := newRealManager(t)
	h := newPluginsHarness(t, func() (*plugins.Manager, error) { return m, nil })
	for _, ep := range []string{"/plugins/preview", "/plugins/install", "/plugins/settings"} {
		if r := h.call(t, http.MethodPost, ep, "{not json"); r.Success {
			t.Errorf("POST %s accepted a malformed body", ep)
		}
	}
}

func TestPluginsArchiveBaseSetting(t *testing.T) {
	m := newRealManager(t)
	h := newPluginsHarness(t, func() (*plugins.Manager, error) { return m, nil })
	if r := h.call(t, http.MethodPost, "/plugins/settings", map[string]string{"archiveBase": "http://evil.example"}); r.Success {
		t.Fatal("plain-http non-loopback mirror accepted")
	}
	if r := h.call(t, http.MethodPost, "/plugins/settings", map[string]string{"archiveBase": "https://mirror.example/gh/"}); !r.Success {
		t.Fatalf("valid mirror rejected: %s", r.Msg)
	}
	s := h.state(t)
	if s.CustomArchiveBase != "https://mirror.example/gh" || s.ArchiveBase != "https://mirror.example/gh" {
		t.Errorf("state = %+v", s)
	}
	if r := h.call(t, http.MethodPost, "/plugins/settings", map[string]string{"archiveBase": ""}); !r.Success {
		t.Fatalf("clearing the mirror failed: %s", r.Msg)
	}
	if s := h.state(t); s.CustomArchiveBase != "" || s.ArchiveBase == "https://mirror.example/gh" {
		t.Errorf("mirror not cleared: %+v", s)
	}
}

// stubRunner is a minimal Runner: units appear on Start and vanish on Stop.
type stubRunner struct {
	mu          sync.Mutex
	socketDir   string
	unavailable error
	units       map[string]plugins.ProcessSpec
	logs        []string
}

func newStubRunner() *stubRunner {
	return &stubRunner{units: map[string]plugins.ProcessSpec{}, logs: []string{"first line", "second line"}}
}

func (r *stubRunner) Available() error { return r.unavailable }
func (r *stubRunner) Isolation() plugins.Isolation {
	return plugins.NewIsolation("stub", false, true, true)
}
func (r *stubRunner) SecretsDir() string { return "" }

func (r *stubRunner) Paths(id string) (string, string) {
	if r.socketDir != "" {
		return filepath.Join(r.socketDir, id+".sock"), "/var/lib/stub/" + id
	}
	return "/run/stub/" + id + ".sock", "/var/lib/stub/" + id
}

func (r *stubRunner) Start(_ context.Context, s plugins.ProcessSpec) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.units[s.ID] = s
	return nil
}

func (r *stubRunner) Stop(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.units, id)
	return nil
}

func (r *stubRunner) Status(_ context.Context, id string) (plugins.RunStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.units[id]; ok {
		return plugins.RunStatus{ID: id, State: plugins.StateRunning, Fingerprint: s.Fingerprint, PID: 4242}, nil
	}
	return plugins.RunStatus{ID: id, State: plugins.StateStopped}, nil
}

func (r *stubRunner) List(ctx context.Context) ([]plugins.RunStatus, error) {
	r.mu.Lock()
	ids := make([]string, 0, len(r.units))
	for id := range r.units {
		ids = append(ids, id)
	}
	r.mu.Unlock()
	var out []plugins.RunStatus
	for _, id := range ids {
		st, _ := r.Status(ctx, id)
		out = append(out, st)
	}
	return out, nil
}
func (r *stubRunner) Logs(context.Context, string, int) ([]string, error) { return r.logs, nil }

func newProcessHarness(t *testing.T, runner *stubRunner) (*pluginsHarness, *plugins.Manager, *plugins.Supervisor) {
	t.Helper()
	m := newRealManager(t)
	sup := plugins.NewSupervisor(m, runner, plugins.SupervisorOptions{
		GOARCH: "amd64",
		Health: func(context.Context, string) error { return nil },
	})
	h := newPluginsHarnessWith(t, &service.PluginService{
		Provider:   func() (*plugins.Manager, error) { return m, nil },
		Supervisor: func() *plugins.Supervisor { return sup },
	})
	p := previewOK(t, h)
	if r := h.call(t, http.MethodPost, "/plugins/install", map[string]any{"token": p.Token, "permissions": p.Manifest.Permissions}); !r.Success {
		t.Fatalf("install: %s", r.Msg)
	}
	return h, m, sup
}

func TestPluginsListShowsProcessRuntime(t *testing.T) {
	h, _, sup := newProcessHarness(t, newStubRunner())

	s := h.state(t)
	if !s.ProcessesAvailable || s.ProcessesError != "" {
		t.Errorf("processes availability = %v %q", s.ProcessesAvailable, s.ProcessesError)
	}
	if iso := s.ProcessesIsolation; iso == nil || iso.Runner != "stub" || iso.Level != plugins.IsolationPartial || iso.Network {
		t.Errorf("processesIsolation = %+v, want the runner's own report so the UI can say what is not enforced", iso)
	}
	if s.Plugins[0].Runtime == nil || s.Plugins[0].Runtime.State != plugins.StateStopped {
		t.Fatalf("a disabled process plugin reads as stopped: %+v", s.Plugins[0].Runtime)
	}

	if r := h.call(t, http.MethodPost, "/plugins/enable/traffic-alerts", nil); !r.Success {
		t.Fatalf("enable: %s", r.Msg)
	}
	sup.Reconcile(context.Background())
	rt := h.state(t).Plugins[0].Runtime
	if rt == nil || rt.State != plugins.StateRunning || !rt.Healthy || rt.PID != 4242 {
		t.Errorf("runtime after reconcile = %+v", rt)
	}
}

func TestPluginsListWithoutProcessSupport(t *testing.T) {
	runner := newStubRunner()
	runner.unavailable = errors.New("this system was not booted with systemd")
	h, _, _ := newProcessHarness(t, runner)
	s := h.state(t)
	if s.ProcessesAvailable || !strings.Contains(s.ProcessesError, "systemd") {
		t.Errorf("state = %+v", s)
	}
	if !s.Available || len(s.Plugins) != 1 {
		t.Errorf("installing must keep working without process support: %+v", s)
	}
}

func TestPluginsLogsAndRestart(t *testing.T) {
	runner := newStubRunner()
	h, _, sup := newProcessHarness(t, runner)

	if r := h.call(t, http.MethodPost, "/plugins/restart/traffic-alerts", nil); r.Success {
		t.Error("restarting a disabled plugin succeeded")
	}
	if r := h.call(t, http.MethodPost, "/plugins/enable/traffic-alerts", nil); !r.Success {
		t.Fatal(r.Msg)
	}
	sup.Reconcile(context.Background())
	if r := h.call(t, http.MethodPost, "/plugins/restart/traffic-alerts", nil); !r.Success {
		t.Fatalf("restart: %s", r.Msg)
	}

	logs := h.call(t, http.MethodGet, "/plugins/logs/traffic-alerts?lines=50", nil)
	var lines []string
	if err := json.Unmarshal(logs.Obj, &lines); !logs.Success || err != nil || len(lines) != 2 || lines[0] != "first line" {
		t.Errorf("logs = %s (%v)", logs.Obj, err)
	}
	if r := h.call(t, http.MethodGet, "/plugins/logs/missing", nil); r.Success {
		t.Error("logs of an unknown plugin succeeded")
	}
	if r := h.call(t, http.MethodGet, "/plugins/logs/traffic-alerts?lines=abc", nil); r.Success {
		t.Error("a non-numeric lines parameter was accepted")
	}

	runner.unavailable = errors.New("no systemd")
	if r := h.call(t, http.MethodGet, "/plugins/logs/traffic-alerts", nil); r.Success || !strings.Contains(r.Msg, "no systemd") {
		t.Errorf("logs without a runner: %+v", r)
	}
}

func TestPluginsEnableAndDisableKickTheSupervisor(t *testing.T) {
	runner := newStubRunner()
	m := newRealManager(t)
	sup := plugins.NewSupervisor(m, runner, plugins.SupervisorOptions{GOARCH: "amd64", Interval: time.Hour, Health: func(context.Context, string) error { return nil }})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { sup.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	h := newPluginsHarnessWith(t, &service.PluginService{
		Provider:   func() (*plugins.Manager, error) { return m, nil },
		Supervisor: func() *plugins.Supervisor { return sup },
	})
	p := previewOK(t, h)
	h.call(t, http.MethodPost, "/plugins/install", map[string]any{"token": p.Token, "permissions": p.Manifest.Permissions})

	h.call(t, http.MethodPost, "/plugins/enable/traffic-alerts", nil)
	waitUntil(t, "the enabled plugin to start without waiting for the timer", func() bool {
		st, _ := runner.Status(context.Background(), "traffic-alerts")
		return st.State == plugins.StateRunning
	})
	h.call(t, http.MethodPost, "/plugins/disable/traffic-alerts", nil)
	waitUntil(t, "the disabled plugin to stop", func() bool {
		st, _ := runner.Status(context.Background(), "traffic-alerts")
		return st.State == plugins.StateStopped
	})
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// closeNotifyRecorder adds the http.CloseNotifier gin's writer asks the
// underlying ResponseWriter for; a real server connection has it.
type closeNotifyRecorder struct{ *httptest.ResponseRecorder }

func (closeNotifyRecorder) CloseNotify() <-chan bool { return make(chan bool) }

func rawCall(h *pluginsHarness, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := closeNotifyRecorder{httptest.NewRecorder()}
	h.engine.ServeHTTP(rec, req)
	return rec.ResponseRecorder
}

func TestPluginsProxyReachesTheRunningProcess(t *testing.T) {
	sockDir, err := os.MkdirTemp("", "lucxc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	runner := newStubRunner()
	runner.socketDir = sockDir
	h, _, sup := newProcessHarness(t, runner)

	var mu sync.Mutex
	var got struct{ method, path, query, body, user string }
	ln, err := net.Listen("unix", filepath.Join(sockDir, "traffic-alerts.sock"))
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got.method, got.path, got.query, got.body, got.user = r.Method, r.URL.Path, r.URL.RawQuery, string(b), r.Header.Get("X-Lucx-User")
		mu.Unlock()
		_, _ = w.Write([]byte(`{"pong":true}`))
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	if rec := rawCall(h, http.MethodGet, "/plugins/x/traffic-alerts/api/ping", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("a disabled plugin: status = %d, want 503", rec.Code)
	}
	if r := h.call(t, http.MethodPost, "/plugins/enable/traffic-alerts", nil); !r.Success {
		t.Fatal(r.Msg)
	}
	sup.Reconcile(context.Background())

	rec := rawCall(h, http.MethodPost, "/plugins/x/traffic-alerts/api/ping?x=1", "hello")
	if rec.Code != http.StatusOK || rec.Body.String() != `{"pong":true}` {
		t.Fatalf("proxy = %d %q", rec.Code, rec.Body.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if got.method != http.MethodPost || got.path != "/api/ping" || got.query != "x=1" || got.body != "hello" || got.user != "api-token" {
		t.Errorf("the plugin saw %+v", got)
	}
	for _, c := range []struct {
		path string
		want int
	}{
		{"/plugins/x/traffic-alerts/_lucx/health", http.StatusNotFound},
		{"/plugins/x/missing-plugin/api/ping", http.StatusNotFound},
	} {
		if r := rawCall(h, http.MethodGet, c.path, ""); r.Code != c.want {
			t.Errorf("GET %s = %d, want %d", c.path, r.Code, c.want)
		}
	}
}

// masterHarness serves the plugin API the way a node does for a master panel:
// the request carries the node-sync scope.
func newScopedHarness(t *testing.T, scope string, m *plugins.Manager, sup *plugins.Supervisor) *pluginsHarness {
	t.Helper()
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
	svc := &service.PluginService{Provider: func() (*plugins.Manager, error) { return m, nil }}
	if sup != nil {
		svc.Supervisor = func() *plugins.Supervisor { return sup }
	}
	NewPluginsController(api.Group("/plugins"), svc)
	return &pluginsHarness{engine: engine}
}

func (h *pluginsHarness) callAt(t *testing.T, method, path string, body any) pluginsResp {
	return h.call(t, method, "/panel/api"+path, body)
}

func pinnedBody(t *testing.T) map[string]any {
	t.Helper()
	manifest := `{"id":"traffic-alerts","name":"Traffic Alerts","version":"1.2.0","apiVersion":"1","type":"process",` +
		`"run":{"command":"bin/${arch}/alerts"},"permissions":["api:clients:read","network"],"scope":"node"}`
	sum := sha256.Sum256([]byte(manifest))
	return map[string]any{
		"url": "https://github.com/acme/alerts", "commit": pluginsTestCommit,
		"manifestSha256": hex.EncodeToString(sum[:]), "permissions": []string{"api:clients:read", "network"},
	}
}

func newNodeManager(t *testing.T) *plugins.Manager {
	t.Helper()
	manifest := `{"id":"traffic-alerts","name":"Traffic Alerts","version":"1.2.0","apiVersion":"1","type":"process",` +
		`"run":{"command":"bin/${arch}/alerts"},"permissions":["api:clients:read","network"],"scope":"node"}`
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeXGlobalHeader, Name: "pax_global_header", PAXRecords: map[string]string{"comment": pluginsTestCommit}})
	for name, body := range map[string]string{"alerts-0123456/lucx-plugin.json": manifest, "alerts-0123456/bin/amd64/alerts": "ELF"} {
		_ = tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(body))})
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = gz.Close()
	archive := buf.Bytes()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/acme/alerts/tar.gz/"+pluginsTestCommit && r.URL.Path != "/acme/alerts/tar.gz/HEAD" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(archive)
	}))
	t.Cleanup(srv.Close)
	root := t.TempDir()
	m, err := plugins.NewManager(plugins.Config{
		CodeDir: filepath.Join(root, "plugins"), DataDir: filepath.Join(root, "data"),
		RegistryPath: filepath.Join(root, "plugins.json"), ArchiveBase: srv.URL, HTTPClient: srv.Client(), PanelVersion: "3.10.2",
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestAMasterIsLockedOutUntilTheNodeOwnerAllowsIt(t *testing.T) {
	m := newNodeManager(t)
	master := newScopedHarness(t, model.ApiScopeNodeSync, m, nil)

	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/plugins/list"},
		{http.MethodPost, "/plugins/remote/install"},
		{http.MethodPost, "/plugins/enable/traffic-alerts"},
		{http.MethodGet, "/plugins/logs/traffic-alerts"},
	} {
		r := master.callAt(t, c.method, c.path, pinnedBody(t))
		if r.Success || !strings.Contains(r.Msg, "disabled on this node") {
			t.Errorf("%s %s while the opt-in is off: %+v", c.method, c.path, r)
		}
	}
	if len(m.List()) != 0 {
		t.Fatal("a locked-out master installed something")
	}

	admin := newScopedHarness(t, "", m, nil)
	if r := admin.callAt(t, http.MethodPost, "/plugins/master-management", map[string]bool{"allow": true}); !r.Success {
		t.Fatalf("the node owner could not allow the master: %s", r.Msg)
	}
	if r := master.callAt(t, http.MethodGet, "/plugins/list", nil); !r.Success {
		t.Errorf("list after the opt-in: %s", r.Msg)
	}
	var st stateView
	_ = json.Unmarshal(admin.callAt(t, http.MethodGet, "/plugins/list", nil).Obj, &st)
	if !st.AllowMasterManagement {
		t.Error("the state must show that the master is allowed")
	}
	if r := admin.callAt(t, http.MethodPost, "/plugins/master-management", map[string]bool{"allow": false}); !r.Success {
		t.Fatal(r.Msg)
	}
	if r := master.callAt(t, http.MethodGet, "/plugins/list", nil); r.Success {
		t.Error("revoking the opt-in must lock the master out again at once")
	}
}

func TestAnAllowedMasterCanOnlyUseItsNarrowRoutes(t *testing.T) {
	m := newNodeManager(t)
	if err := m.SetAllowMasterManagement(true); err != nil {
		t.Fatal(err)
	}
	master := newScopedHarness(t, model.ApiScopeNodeSync, m, nil)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/plugins/menu"},
		{http.MethodGet, "/plugins/catalog"},
		{http.MethodPost, "/plugins/catalog/refresh"},
		{http.MethodPost, "/plugins/catalog/preview/traffic-alerts"},
		{http.MethodPost, "/plugins/catalog/update-preview/traffic-alerts"},
		{http.MethodPost, "/plugins/catalog/settings"},
		{http.MethodPost, "/plugins/update-preview/traffic-alerts"},
		{http.MethodPost, "/plugins/update/traffic-alerts"},
		{http.MethodPost, "/plugins/rollback/traffic-alerts"},
		{http.MethodGet, "/plugins/frame-token/traffic-alerts"},
		{http.MethodPost, "/plugins/preview"},
		{http.MethodPost, "/plugins/install"},
		{http.MethodPost, "/plugins/settings"},
		{http.MethodPost, "/plugins/master-management"},
		{http.MethodGet, "/plugins/x/traffic-alerts/anything"},
		{http.MethodPost, "/plugins/x/traffic-alerts/anything"},
	} {
		if r := master.callAt(t, c.method, c.path, map[string]any{"allow": true, "url": "https://github.com/acme/alerts", "archiveBase": "https://evil.example"}); r.Success {
			t.Errorf("%s %s must not be reachable by a master", c.method, c.path)
		}
	}
	if m.AllowMasterManagement() != true || m.CustomArchiveBase() != "" {
		t.Error("a master changed the node's own plugin settings")
	}
}

func TestAMasterInstallsPinnedPluginsAndControlsOnlyItsOwn(t *testing.T) {
	m := newNodeManager(t)
	if err := m.SetAllowMasterManagement(true); err != nil {
		t.Fatal(err)
	}
	master := newScopedHarness(t, model.ApiScopeNodeSync, m, nil)
	admin := newScopedHarness(t, "", m, nil)

	r := master.callAt(t, http.MethodPost, "/plugins/remote/install", pinnedBody(t))
	if !r.Success {
		t.Fatalf("remote install: %s", r.Msg)
	}
	list := m.List()
	if len(list) != 1 || !list[0].ManagedByMaster || list[0].Enabled {
		t.Fatalf("installed = %+v", list)
	}

	bad := pinnedBody(t)
	bad["manifestSha256"] = strings.Repeat("0", 64)
	if r := master.callAt(t, http.MethodPost, "/plugins/remote/install", bad); r.Success {
		t.Error("a manifest the master did not approve was installed")
	}

	if r := master.callAt(t, http.MethodPost, "/plugins/enable/traffic-alerts", nil); !r.Success {
		t.Fatalf("the master must control what it installed: %s", r.Msg)
	}
	if r := master.callAt(t, http.MethodPost, "/plugins/disable/traffic-alerts", nil); !r.Success {
		t.Fatalf("disable: %s", r.Msg)
	}

	// a plugin the node owner installed by hand is not the master's to touch
	local := newNodeManager(t)
	if err := local.SetAllowMasterManagement(true); err != nil {
		t.Fatal(err)
	}
	localAdmin := newScopedHarness(t, "", local, nil)
	p := previewOK(t, newPluginsHarnessWith(t, &service.PluginService{Provider: func() (*plugins.Manager, error) { return local, nil }}))
	if _, err := local.Install(p.Token, p.Manifest.Permissions); err != nil {
		t.Fatal(err)
	}
	localMaster := newScopedHarness(t, model.ApiScopeNodeSync, local, nil)
	for _, ep := range []string{"/plugins/enable/traffic-alerts", "/plugins/disable/traffic-alerts", "/plugins/restart/traffic-alerts", "/plugins/uninstall/traffic-alerts"} {
		if r := localMaster.callAt(t, http.MethodPost, ep, nil); r.Success || !strings.Contains(r.Msg, "installed locally") {
			t.Errorf("master POST %s on a locally installed plugin: %+v", ep, r)
		}
	}
	if r := localMaster.callAt(t, http.MethodGet, "/plugins/logs/traffic-alerts", nil); r.Success {
		t.Error("the master read the log of a locally installed plugin")
	}
	if r := localAdmin.callAt(t, http.MethodPost, "/plugins/enable/traffic-alerts", nil); !r.Success {
		t.Errorf("the node owner can always control it: %s", r.Msg)
	}

	if r := master.callAt(t, http.MethodPost, "/plugins/uninstall/traffic-alerts", map[string]bool{"removeData": true}); !r.Success {
		t.Fatalf("uninstall: %s", r.Msg)
	}
	if len(m.List()) != 0 {
		t.Error("the plugin is still installed")
	}
	_ = admin
}

func TestAPinnedInstallFinishesAfterTheMasterHangsUp(t *testing.T) {
	m := newNodeManager(t)
	if err := m.SetAllowMasterManagement(true); err != nil {
		t.Fatal(err)
	}
	master := newScopedHarness(t, model.ApiScopeNodeSync, m, nil)
	body, err := json.Marshal(pinnedBody(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/panel/api/plugins/remote/install", bytes.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	master.engine.ServeHTTP(closeNotifyRecorder{httptest.NewRecorder()}, req)

	if list := m.List(); len(list) != 1 {
		t.Fatalf("installed = %+v: the master's 10 s timeout must not abandon a download the node already started", list)
	}
}
