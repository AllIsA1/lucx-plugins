// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"archive/tar"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Real units in the user systemd manager, driven through Manager + Supervisor:
// start, adoption by a second supervisor (a panel restart), disable, uninstall.
func TestSupervisorUserModeE2E(t *testing.T) {
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
	codeDir, err := os.MkdirTemp(filepath.Join(home, ".cache"), "lucx-e2e-sup-")
	if err != nil {
		t.Skipf("cannot create a code dir under ~/.cache: %v", err)
	}

	root := "alerts-" + testCommit[:7] + "/"
	archive := buildArchive(t, testCommit,
		dir(root),
		file(root+"lucx-plugin.json", string(manifestJSON(t, nil))),
		tarEntry{name: root + "bin/" + runtime.GOARCH + "/alerts", typ: tar.TypeReg, body: "#!/bin/sh\nexec sleep 300\n", mode: 0o755},
	)
	env := newEnv(t, map[string][]byte{"/acme/alerts/tar.gz/HEAD": archive})
	cfg := env.cfg
	cfg.CodeDir = codeDir
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	t.Cleanup(func() {
		_ = runner.Stop(ctx, "traffic-alerts")
		_ = os.RemoveAll(codeDir)
		_, data := runner.Paths("traffic-alerts")
		_ = os.RemoveAll(data)
	})

	p, err := m.Preview(ctx, alertsURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Install(p.Token, p.Manifest.Permissions); err != nil {
		t.Fatal(err)
	}
	if err := m.SetEnabled("traffic-alerts", true); err != nil {
		t.Fatal(err)
	}

	opts := SupervisorOptions{GOARCH: runtime.GOARCH}
	first := NewSupervisor(m, runner, opts)
	first.Reconcile(ctx)
	waitFor(t, "the plugin process", func() bool { st, _ := runner.Status(ctx, "traffic-alerts"); return st.State == StateRunning })
	before, _ := runner.Status(ctx, "traffic-alerts")
	if before.Fingerprint != Fingerprint(m.List()[0]) {
		t.Fatalf("unit fingerprint %q, want %q", before.Fingerprint, Fingerprint(m.List()[0]))
	}

	second := NewSupervisor(m, runner, opts)
	second.Reconcile(ctx)
	if after, _ := runner.Status(ctx, "traffic-alerts"); after.PID != before.PID {
		t.Errorf("a new supervisor restarted the plugin instead of adopting it: pid %d -> %d", before.PID, after.PID)
	}
	if rt, ok := second.Runtime("traffic-alerts"); !ok || rt.State != StateRunning || rt.PID != before.PID {
		t.Errorf("runtime after adoption = %+v, %v", rt, ok)
	}

	if err := m.SetEnabled("traffic-alerts", false); err != nil {
		t.Fatal(err)
	}
	second.Reconcile(ctx)
	waitFor(t, "stop after disable", func() bool { st, _ := runner.Status(ctx, "traffic-alerts"); return st.State == StateStopped })

	if err := m.SetEnabled("traffic-alerts", true); err != nil {
		t.Fatal(err)
	}
	second.Reconcile(ctx)
	waitFor(t, "restart after re-enable", func() bool { st, _ := runner.Status(ctx, "traffic-alerts"); return st.State == StateRunning })
	if err := m.Uninstall("traffic-alerts", true); err != nil {
		t.Fatalf("Uninstall of a running plugin: %v", err)
	}
	if st, _ := runner.Status(ctx, "traffic-alerts"); st.State != StateStopped {
		t.Errorf("the process survived uninstall: %+v", st)
	}
	if _, err := os.Stat(filepath.Join(codeDir, "traffic-alerts")); !os.IsNotExist(err) {
		t.Error("code directory still exists after uninstall")
	}
}

// A plugin that exits at once must end up disabled with a reason, not be
// restarted for ever: this is why the unit must not be collected when it fails.
func TestSupervisorUserModeDisablesCrashLoopE2E(t *testing.T) {
	if os.Getenv("XUI_PLUGINS_SYSTEMD_E2E") != "1" {
		t.Skip("set XUI_PLUGINS_SYSTEMD_E2E=1 to run against the user systemd manager")
	}
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	home, err := os.UserHomeDir()
	if err != nil || runtimeDir == "" {
		t.Skip("no user session")
	}
	runner := newUserSystemdRunner(filepath.Join(runtimeDir, "x-ui-plugins"), filepath.Join(home, ".local", "state", "x-ui-plugins"))
	runner.restartSec = "100ms"
	if err := runner.Available(); err != nil {
		t.Skipf("user systemd unavailable: %v", err)
	}
	codeDir, err := os.MkdirTemp(filepath.Join(home, ".cache"), "lucx-e2e-crash-")
	if err != nil {
		t.Skipf("cannot create a code dir under ~/.cache: %v", err)
	}
	root := "alerts-" + testCommit[:7] + "/"
	archive := buildArchive(t, testCommit,
		dir(root),
		file(root+"lucx-plugin.json", string(manifestJSON(t, nil))),
		tarEntry{name: root + "bin/" + runtime.GOARCH + "/alerts", typ: tar.TypeReg, body: "#!/bin/sh\necho boom\nexit 1\n", mode: 0o755},
	)
	env := newEnv(t, map[string][]byte{"/acme/alerts/tar.gz/HEAD": archive})
	cfg := env.cfg
	cfg.CodeDir = codeDir
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	t.Cleanup(func() {
		_ = runner.Stop(ctx, "traffic-alerts")
		_ = os.RemoveAll(codeDir)
	})
	p, err := m.Preview(ctx, alertsURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Install(p.Token, p.Manifest.Permissions); err != nil {
		t.Fatal(err)
	}
	if err := m.SetEnabled("traffic-alerts", true); err != nil {
		t.Fatal(err)
	}

	sup := NewSupervisor(m, runner, SupervisorOptions{GOARCH: runtime.GOARCH})
	waitFor(t, "the crash loop to be disabled", func() bool {
		sup.Reconcile(ctx)
		return !m.List()[0].Enabled
	})
	got := m.List()[0]
	if !strings.Contains(got.DisabledReason, "keeps failing") {
		t.Errorf("DisabledReason = %q", got.DisabledReason)
	}
	sup.Reconcile(ctx)
	if st, _ := runner.Status(ctx, "traffic-alerts"); st.State != StateStopped {
		t.Errorf("a disabled plugin must leave no failed unit behind: %+v", st)
	}
}
