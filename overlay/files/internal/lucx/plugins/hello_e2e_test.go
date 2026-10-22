// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"archive/tar"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/eventbus"
)

// The whole chain on real units: build examples/hello-plugin, install it
// through the Manager, run it under the user systemd manager, then use it
// through the proxy and let it call a panel stub with its issued token.
func TestHelloExampleE2E(t *testing.T) {
	if os.Getenv("XUI_PLUGINS_SYSTEMD_E2E") != "1" {
		t.Skip("set XUI_PLUGINS_SYSTEMD_E2E=1 to run against the user systemd manager")
	}
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	home, err := os.UserHomeDir()
	if err != nil || runtimeDir == "" {
		t.Skip("no user session")
	}
	runner := newUserSystemdRunner(filepath.Join(runtimeDir, "x-ui-plugins"), filepath.Join(home, ".local", "state", "x-ui-plugins"))
	if err := runner.Available(); err != nil {
		t.Skipf("user systemd unavailable: %v", err)
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not found")
	}

	example, err := filepath.Abs(filepath.Join("..", "..", "..", "examples", "hello-plugin"))
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "hello")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = example
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the example: %v\n%s", err, out)
	}
	binBytes, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(example, ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	root := "hello-0123456/"
	archive := buildArchive(t, testCommit,
		dir(root),
		file(root+ManifestFile, string(manifest)),
		tarEntry{name: root + "bin/" + runtime.GOARCH + "/hello", typ: tar.TypeReg, body: string(binBytes), mode: 0o755},
	)
	env := newEnv(t, map[string][]byte{"/acme/hello/tar.gz/HEAD": archive})

	codeDir, err := os.MkdirTemp(filepath.Join(home, ".cache"), "lucx-e2e-hello-")
	if err != nil {
		t.Skipf("cannot create a code dir under ~/.cache: %v", err)
	}
	cfg := env.cfg
	cfg.CodeDir = codeDir
	cfg.Limits = DefaultLimits
	var m *Manager
	panelStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		e, ok := m.VerifyToken(token)
		if !ok || r.URL.Path != "/panel/api/clients/list" || !AllowAPI(e.Permissions, r.Method, "/clients/list") {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "obj": []map[string]string{{"email": "a"}, {"email": "b"}}})
	}))
	t.Cleanup(panelStub.Close)
	cfg.PanelURL = func() string { return panelStub.URL }
	m, err = NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	t.Cleanup(func() {
		_ = runner.Stop(ctx, "hello")
		_ = os.RemoveAll(codeDir)
		_, data := runner.Paths("hello")
		_ = os.RemoveAll(data)
		_ = os.RemoveAll(runner.SecretsDir())
	})

	p, err := m.Preview(ctx, "https://github.com/acme/hello")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Install(p.Token, p.Manifest.Permissions); err != nil {
		t.Fatal(err)
	}
	if err := m.SetEnabled("hello", true); err != nil {
		t.Fatal(err)
	}
	sup := NewSupervisor(m, runner, SupervisorOptions{GOARCH: runtime.GOARCH})
	waitFor(t, "a healthy hello plugin", func() bool {
		sup.Reconcile(ctx)
		rt, _ := sup.Runtime("hello")
		return rt.State == StateRunning && rt.Healthy
	})

	get := func(method, path string) (int, string) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/x", nil)
		if err := sup.Proxy(w, req, "hello", path, "admin"); err != nil {
			t.Fatalf("Proxy %s %s: %v", method, path, err)
		}
		return w.Code, strings.TrimSpace(w.Body.String())
	}
	if code, body := get(http.MethodGet, "/hello"); code != 200 || body != `{"message":"hello","user":"admin"}` {
		t.Errorf("/hello = %d %s", code, body)
	}
	if code, body := get(http.MethodGet, "/clients"); code != 200 || body != `{"clients":2}` {
		t.Errorf("/clients (the plugin called the panel with its token) = %d %s", code, body)
	}
	if code, body := get(http.MethodPost, "/counter"); code != 200 || body != `{"counter":1}` {
		t.Errorf("first /counter = %d %s", code, body)
	}

	sup.Dispatch(eventbus.Event{Type: eventbus.EventClientCreated, Source: "alice@example.com", Data: eventbus.ClientEventData{Email: "alice@example.com"}})
	sup.Dispatch(eventbus.Event{Type: eventbus.EventClientDeleted, Source: "bob@example.com"})
	waitFor(t, "the plugin to receive client.created", func() bool {
		_, body := get(http.MethodGet, "/events")
		return strings.Contains(body, "alice@example.com")
	})
	if _, body := get(http.MethodGet, "/events"); strings.Contains(body, "bob@example.com") || strings.Count(body, `"type":"client.`) != 1 {
		t.Errorf("only the subscribed event may arrive: %s", body)
	}

	if err := sup.Restart(ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "hello to come back healthy", func() bool {
		sup.Reconcile(ctx)
		rt, _ := sup.Runtime("hello")
		return rt.State == StateRunning && rt.Healthy
	})
	if code, body := get(http.MethodPost, "/counter"); code != 200 || body != `{"counter":2}` {
		t.Errorf("the counter must survive a restart: %d %s", code, body)
	}

	if err := m.SetEnabled("hello", false); err != nil {
		t.Fatal(err)
	}
	sup.Reconcile(ctx)
	if _, err := os.Stat(filepath.Join(runner.SecretsDir(), "hello")); !os.IsNotExist(err) {
		t.Error("the token file must be gone after the plugin stopped")
	}
}
