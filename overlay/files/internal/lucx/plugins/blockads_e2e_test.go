// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"archive/tar"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A pipeline plugin on a real unit: the panel asks it for a patch of an Xray
// config document and applies the answer.
func TestBlockAdsExampleE2E(t *testing.T) {
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
	example, err := filepath.Abs(filepath.Join("..", "..", "..", "examples", "block-ads-plugin"))
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "blockads")
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
	root := "block-ads-0123456/"
	archive := buildArchive(t, testCommit,
		dir(root),
		file(root+ManifestFile, string(manifest)),
		tarEntry{name: root + "bin/" + runtime.GOARCH + "/blockads", typ: tar.TypeReg, body: string(binBytes), mode: 0o755},
	)
	env := newEnv(t, map[string][]byte{"/acme/block-ads/tar.gz/HEAD": archive})
	codeDir, err := os.MkdirTemp(filepath.Join(home, ".cache"), "lucx-e2e-ads-")
	if err != nil {
		t.Skipf("cannot create a code dir under ~/.cache: %v", err)
	}
	cfg := env.cfg
	cfg.CodeDir = codeDir
	cfg.Limits = DefaultLimits
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	t.Cleanup(func() {
		_ = runner.Stop(ctx, "block-ads")
		_ = os.RemoveAll(codeDir)
		_, data := runner.Paths("block-ads")
		_ = os.RemoveAll(data)
		_ = os.RemoveAll(runner.SecretsDir())
	})

	p, err := m.Preview(ctx, "https://github.com/acme/block-ads")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Dangerous) != 1 || p.Dangerous[0] != "pipeline:xray.config" {
		t.Errorf("a pipeline permission must be flagged for the operator: %v", p.Dangerous)
	}
	if _, err := m.Install(p.Token, p.Manifest.Permissions); err != nil {
		t.Fatal(err)
	}
	if err := m.SetEnabled("block-ads", true); err != nil {
		t.Fatal(err)
	}
	sup := NewSupervisor(m, runner, SupervisorOptions{GOARCH: runtime.GOARCH})
	t.Cleanup(sup.Close)
	waitFor(t, "a healthy block-ads plugin", func() bool {
		sup.Reconcile(ctx)
		rt, _ := sup.Runtime("block-ads")
		return rt.State == StateRunning && rt.Healthy
	})
	if !sup.HasPipeline(PipelineXrayConfig) {
		t.Fatal("the plugin holds pipeline:xray.config")
	}

	doc := `{"routing":{"rules":[{"outboundTag":"direct"}]},"outbounds":[{"tag":"direct"},{"tag":"blocked"}]}`
	out, res := sup.Transform(ctx, PipelineXrayConfig, []byte(doc), nil)
	if len(res) != 1 || res[0].Err != nil || res[0].Applied != 1 {
		t.Fatalf("results = %+v", res)
	}
	if !strings.Contains(string(out), `"domain":["geosite:category-ads-all"]`) || strings.Index(string(out), "geosite") > strings.Index(string(out), `"outboundTag":"direct"`) {
		t.Errorf("the ads rule must be added in the first position: %s", out)
	}
	again, res := sup.Transform(ctx, PipelineXrayConfig, out, nil)
	if string(again) != string(out) || res[0].Applied != 0 {
		t.Errorf("a document that already has the rule must come back unchanged: %s %+v", again, res)
	}
	plain, _ := sup.Transform(ctx, PipelineXrayConfig, []byte(`{"outbounds":[{"tag":"direct"}]}`), nil)
	if strings.Contains(string(plain), "geosite") {
		t.Errorf("without a blocked outbound nothing may be added: %s", plain)
	}
}
