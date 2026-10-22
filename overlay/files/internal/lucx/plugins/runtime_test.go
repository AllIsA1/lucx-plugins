// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func startConfig(t *testing.T) Config {
	t.Helper()
	root := t.TempDir()
	t.Cleanup(Stop)
	return Config{
		CodeDir:      filepath.Join(root, "plugins"),
		DataDir:      filepath.Join(root, "data"),
		RegistryPath: filepath.Join(root, "plugins.json"),
		Runner:       NewUnavailableRunner("no process runner in tests"),
	}
}

func TestSafeModeEnv(t *testing.T) {
	cases := map[string]bool{"": false, "0": false, "false": false, "1": true, "true": true, "TRUE": true, "yes": true, "on": true}
	for value, want := range cases {
		t.Setenv("XUI_PLUGINS_DISABLED", value)
		if got := SafeMode(); got != want {
			t.Errorf("XUI_PLUGINS_DISABLED=%q: SafeMode = %v, want %v", value, got, want)
		}
	}
}

func TestStartInSafeModeTouchesNothing(t *testing.T) {
	t.Setenv("XUI_PLUGINS_DISABLED", "1")
	cfg := startConfig(t)
	Start(cfg)
	if m, err := Get(); m != nil || !errors.Is(err, ErrDisabled) {
		t.Fatalf("Get = %v, %v; want nil, ErrDisabled", m, err)
	}
	if _, err := os.Stat(cfg.CodeDir); !os.IsNotExist(err) {
		t.Error("safe mode must not create plugin directories")
	}
}

func TestStartSurvivesCorruptRegistry(t *testing.T) {
	t.Setenv("XUI_PLUGINS_DISABLED", "")
	cfg := startConfig(t)
	if err := os.WriteFile(cfg.RegistryPath, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	Start(cfg)
	if m, err := Get(); m != nil || !errors.Is(err, ErrCorruptRegistry) {
		t.Fatalf("Get = %v, %v; want nil, ErrCorruptRegistry", m, err)
	}
}

func TestStartStopLifecycle(t *testing.T) {
	t.Setenv("XUI_PLUGINS_DISABLED", "")
	cfg := startConfig(t)
	if m, err := Get(); m != nil || !errors.Is(err, ErrNotStarted) {
		t.Fatalf("before Start: Get = %v, %v; want ErrNotStarted", m, err)
	}
	Start(cfg)
	m, err := Get()
	if err != nil || m == nil {
		t.Fatalf("after Start: Get = %v, %v", m, err)
	}
	Start(cfg)
	if again, _ := Get(); again == nil {
		t.Error("a second Start (panel restart) must keep a working manager")
	}
	Stop()
	if m, err := Get(); m != nil || !errors.Is(err, ErrNotStarted) {
		t.Fatalf("after Stop: Get = %v, %v; want ErrNotStarted", m, err)
	}
}

func enabledAlertsEnv(t *testing.T) (*testEnv, *fakeRunner) {
	t.Helper()
	t.Setenv("XUI_PLUGINS_DISABLED", "")
	env := newEnv(t, map[string][]byte{"/acme/alerts/tar.gz/HEAD": procArchive(t, nil)})
	t.Cleanup(Stop) // registered after the temp dirs, so it runs before they are removed
	installAlerts(t, env)
	if err := env.m.SetEnabled("traffic-alerts", true); err != nil {
		t.Fatal(err)
	}
	return env, newFakeRunner()
}

func TestStopLeavesProcessesRunningAndTheNextStartAdoptsThem(t *testing.T) {
	env, runner := enabledAlertsEnv(t)
	cfg := env.cfg
	cfg.Runner = runner

	Start(cfg)
	waitFor(t, "the first start", func() bool { return runner.running("traffic-alerts") })
	pid := runner.pid("traffic-alerts")

	Stop()
	if !runner.running("traffic-alerts") || runner.pid("traffic-alerts") != pid {
		t.Fatal("stopping the panel side must not stop plugin processes")
	}
	if GetSupervisor() != nil {
		t.Error("the supervisor must be gone after Stop")
	}

	Start(cfg)
	waitFor(t, "the supervisor after a panel restart", func() bool { return GetSupervisor() != nil })
	waitFor(t, "the first reconcile after the restart", func() bool {
		rt, ok := GetSupervisor().Runtime("traffic-alerts")
		return ok && rt.State == StateRunning
	})
	if runner.pid("traffic-alerts") != pid || runner.startCalls != 1 {
		t.Errorf("a panel restart must adopt the running process (pid %d -> %d, start calls %d)", pid, runner.pid("traffic-alerts"), runner.startCalls)
	}
}

func TestStartMakesTheManagerFollowTheRunnersDataRoot(t *testing.T) {
	env, runner := enabledAlertsEnv(t)
	cfg := env.cfg
	cfg.Runner = runner
	cfg.DataDir = "/somewhere/else"

	Start(cfg)
	m, err := Get()
	if err != nil {
		t.Fatal(err)
	}
	if got := m.cfg.DataDir; got != "/var/lib/fake" {
		t.Errorf("DataDir = %q, want the runner's root so uninstall removes what the process wrote", got)
	}
}

func TestSafeModeStopsRunningPluginUnits(t *testing.T) {
	env, runner := enabledAlertsEnv(t)
	runner.adopt("traffic-alerts", "x")
	t.Setenv("XUI_PLUGINS_DISABLED", "1")
	cfg := env.cfg
	cfg.Runner = runner

	Start(cfg)
	if runner.running("traffic-alerts") {
		t.Error("safe mode must stop plugin processes, not only skip loading them")
	}
	if GetSupervisor() != nil {
		t.Error("no supervisor in safe mode")
	}
}

func TestAuthenticateTokenFollowsTheRegistry(t *testing.T) {
	if _, ok := AuthenticateToken(TokenPrefix + "traffic-alerts.AAAA"); ok {
		t.Fatal("no manager is running, no token can be valid")
	}
	env, runner := enabledAlertsEnv(t)
	cfg := env.cfg
	cfg.Runner = runner
	Start(cfg)
	m, err := Get()
	if err != nil {
		t.Fatal(err)
	}
	entry := m.List()[0]
	tok := m.IssueToken(entry)

	got, ok := AuthenticateToken(tok)
	if !ok || got.ID != "traffic-alerts" || !SamePermissionSet(got.Permissions, entry.Permissions) {
		t.Fatalf("AuthenticateToken = %+v, %v", got, ok)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(cfg.RegistryPath), "plugins.key")); err != nil {
		t.Errorf("the signing key must live beside the registry: %v", err)
	}

	if err := m.SetEnabled("traffic-alerts", false); err != nil {
		t.Fatal(err)
	}
	if _, ok := AuthenticateToken(tok); ok {
		t.Error("a disabled plugin's token must stop working at once")
	}
	if err := m.SetEnabled("traffic-alerts", true); err != nil {
		t.Fatal(err)
	}
	Stop()
	Start(cfg)
	if _, ok := AuthenticateToken(tok); !ok {
		t.Error("tokens must survive a panel restart (the key is persistent)")
	}
}

// childRunner models a runner whose plugins cannot outlive the panel.
type childRunner struct {
	*fakeRunner
	stopAllCalls int
}

func (c *childRunner) StopAll(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopAllCalls++
	clear(c.units)
	return nil
}

func TestStopEndsTheProcessesOfRunnersThatDieWithThePanel(t *testing.T) {
	env, fake := enabledAlertsEnv(t)
	runner := &childRunner{fakeRunner: fake}
	cfg := env.cfg
	cfg.Runner = runner

	Start(cfg)
	waitFor(t, "the first start", func() bool { return fake.running("traffic-alerts") })
	Stop()
	if runner.stopAllCalls != 1 || fake.running("traffic-alerts") {
		t.Errorf("StopAll calls = %d, running = %v; a runner without adoption must be stopped with the panel", runner.stopAllCalls, fake.running("traffic-alerts"))
	}
}

func TestDefaultRunnerSelection(t *testing.T) {
	cfg := Config{DataDir: filepath.Join(t.TempDir(), "plugins-data")}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	cases := []struct {
		name, runner, docker string
		check                func(Runner) bool
	}{
		{"systemd by default", "", "", func(r Runner) bool { return isSystemd(r, false) }},
		{"process runner in the Docker image", "", "true", func(r Runner) bool { _, ok := r.(*ProcessRunner); return ok }},
		{"explicit systemd wins in Docker", "systemd", "true", func(r Runner) bool { return isSystemd(r, false) }},
		{"explicit process runner", "process", "", func(r Runner) bool { _, ok := r.(*ProcessRunner); return ok }},
		{"user systemd", "systemd-user", "", func(r Runner) bool { return isSystemd(r, true) }},
		{"a typo is not silently systemd", "sistemd", "", func(r Runner) bool {
			err := r.Available()
			return err != nil && strings.Contains(err.Error(), `"sistemd"`)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("XUI_PLUGINS_RUNNER", c.runner)
			t.Setenv("XUI_IN_DOCKER", c.docker)
			if r := defaultRunner(cfg); !c.check(r) {
				t.Errorf("defaultRunner = %T", r)
			}
		})
	}
	t.Setenv("XUI_PLUGINS_RUNNER", "process")
	if socket, _ := defaultRunner(cfg).Paths("hello"); socket != "/run/x-ui-plugins/hello/plugin.sock" {
		t.Errorf("default socket = %q", socket)
	}
	runtimeDir := t.TempDir()
	t.Setenv("XUI_PLUGINS_RUNTIME_DIR", runtimeDir)
	if socket, _ := defaultRunner(cfg).Paths("hello"); socket != filepath.Join(runtimeDir, "hello", "plugin.sock") {
		t.Errorf("XUI_PLUGINS_RUNTIME_DIR ignored: socket = %q", socket)
	}
	_, data := defaultRunner(cfg).Paths("hello")
	if want := filepath.Join(cfg.DataDir, "hello"); data != want {
		t.Errorf("process runner data dir = %q, want %q (the configured data root)", data, want)
	}
}

func isSystemd(r Runner, user bool) bool {
	s, ok := r.(*SystemdRunner)
	return ok && s.user == user
}
