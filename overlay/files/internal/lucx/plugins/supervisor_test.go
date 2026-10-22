// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"archive/tar"
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRunner models what the supervisor relies on from systemd: stable units,
// a restart when the fingerprint changes, a failed state after a crash loop.
type fakeRunner struct {
	mu          sync.Mutex
	units       map[string]*fakeUnit
	nextPID     int
	unavailable error
	startErr    error
	socketDir   string
	secretsDir  string
	starts      []string
	startCalls  int
	stops       []string
}

type fakeUnit struct {
	spec  ProcessSpec
	state RunState
	pid   int
	fp    string
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{units: map[string]*fakeUnit{}, nextPID: 1000}
}

func (f *fakeRunner) Available() error { return f.unavailable }

func (f *fakeRunner) Isolation() Isolation { return NewIsolation("fake", true, true, true) }

func (f *fakeRunner) SecretsDir() string { return f.secretsDir }

func (f *fakeRunner) Paths(id string) (string, string) {
	if f.socketDir != "" {
		return filepath.Join(f.socketDir, id+".sock"), "/var/lib/fake/" + id
	}
	return "/run/fake/" + id + "/plugin.sock", "/var/lib/fake/" + id
}

func (f *fakeRunner) Start(_ context.Context, spec ProcessSpec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startCalls++
	if f.startErr != nil {
		return f.startErr
	}
	if u, ok := f.units[spec.ID]; ok && u.state == StateRunning && u.fp == spec.Fingerprint {
		return nil
	}
	f.nextPID++
	f.units[spec.ID] = &fakeUnit{spec: spec, state: StateRunning, pid: f.nextPID, fp: spec.Fingerprint}
	f.starts = append(f.starts, spec.ID)
	return nil
}

func (f *fakeRunner) Stop(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.units, id)
	f.stops = append(f.stops, id)
	return nil
}

func (f *fakeRunner) Status(_ context.Context, id string) (RunStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.statusLocked(id), nil
}

func (f *fakeRunner) statusLocked(id string) RunStatus {
	u, ok := f.units[id]
	if !ok {
		return RunStatus{ID: id, State: StateStopped}
	}
	st := RunStatus{ID: id, State: u.state, Fingerprint: u.fp, PID: u.pid}
	if u.state == StateFailed {
		st.Detail = "start-limit-hit"
	}
	return st
}

func (f *fakeRunner) List(context.Context) ([]RunStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []RunStatus
	for id := range f.units {
		out = append(out, f.statusLocked(id))
	}
	return out, nil
}

func (f *fakeRunner) Logs(context.Context, string, int) ([]string, error) {
	return []string{"hello from the plugin"}, nil
}

func (f *fakeRunner) adopt(id, fingerprint string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextPID++
	f.units[id] = &fakeUnit{state: StateRunning, pid: f.nextPID, fp: fingerprint}
	return f.nextPID
}

func (f *fakeRunner) crash(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.units[id].state = StateFailed
}

func (f *fakeRunner) startCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.starts)
}

func (f *fakeRunner) pid(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.units[id]; ok {
		return u.pid
	}
	return 0
}

func (f *fakeRunner) running(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.units[id]
	return ok && u.state == StateRunning
}

func procArchive(t *testing.T, mutate func(m map[string]any)) []byte {
	t.Helper()
	root := "alerts-" + testCommit[:7] + "/"
	return buildArchive(t, testCommit,
		dir(root),
		file(root+"lucx-plugin.json", string(manifestJSON(t, mutate))),
		tarEntry{name: root + "bin/amd64/alerts", typ: tar.TypeReg, body: "ELF", mode: 0o755},
	)
}

type supEnv struct {
	*testEnv
	runner *fakeRunner
	sup    *Supervisor
}

func newSupEnv(t *testing.T, mutate func(m map[string]any), opts ...func(*SupervisorOptions)) *supEnv {
	t.Helper()
	env := newEnv(t, map[string][]byte{"/acme/alerts/tar.gz/HEAD": procArchive(t, mutate)})
	installAlerts(t, env)
	runner := newFakeRunner()
	o := SupervisorOptions{GOARCH: "amd64", Health: func(context.Context, string) error { return nil }}
	for _, fn := range opts {
		fn(&o)
	}
	return &supEnv{testEnv: env, runner: runner, sup: NewSupervisor(env.m, runner, o)}
}

func (e *supEnv) enable(t *testing.T) {
	t.Helper()
	if err := e.m.SetEnabled("traffic-alerts", true); err != nil {
		t.Fatal(err)
	}
}

func (e *supEnv) entry() Entry { return e.m.List()[0] }

func (e *supEnv) reconcile() { e.sup.Reconcile(context.Background()) }

func TestReconcileStartsOnlyEnabledProcessPlugins(t *testing.T) {
	e := newSupEnv(t, nil)
	e.reconcile()
	if e.runner.running("traffic-alerts") {
		t.Fatal("a disabled plugin was started")
	}

	e.enable(t)
	e.reconcile()
	if !e.runner.running("traffic-alerts") {
		t.Fatal("an enabled process plugin was not started")
	}
	u := e.runner.units["traffic-alerts"]
	wantCmd := filepath.Join(e.codeDir, "traffic-alerts", "bin", "amd64", "alerts")
	if u.spec.Command != wantCmd || u.fp != Fingerprint(e.entry()) {
		t.Errorf("started with command %q fingerprint %q", u.spec.Command, u.fp)
	}
	if u.spec.SocketPath != "/run/fake/traffic-alerts/plugin.sock" || !slices.Contains(u.spec.Args, u.spec.SocketPath) {
		t.Errorf("socket and args must come from the runner paths: %+v", u.spec)
	}
}

func TestReconcileLeavesUIAndNodeScopedPluginsAlone(t *testing.T) {
	ui := newSupEnv(t, func(m map[string]any) {
		m["type"] = "ui"
		delete(m, "run")
		m["permissions"] = []string{"api:clients:read"}
	})
	ui.enable(t)
	ui.reconcile()
	if ui.runner.startCount() != 0 {
		t.Error("a ui plugin has no process to start")
	}
	if _, ok := ui.sup.Runtime("traffic-alerts"); ok {
		t.Error("a ui plugin has no runtime status")
	}

	node := newSupEnv(t, func(m map[string]any) { m["scope"] = "node" })
	node.enable(t)
	node.reconcile()
	if node.runner.startCount() != 0 {
		t.Error("a node-scoped plugin must not run on the master")
	}
}

func TestReconcileStopsDisabledPluginsAndOrphans(t *testing.T) {
	e := newSupEnv(t, nil)
	e.enable(t)
	e.reconcile()
	e.runner.adopt("ghost-plugin", "whatever")

	if err := e.m.SetEnabled("traffic-alerts", false); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	if e.runner.running("traffic-alerts") {
		t.Error("a disabled plugin kept running")
	}
	if e.runner.running("ghost-plugin") {
		t.Error("a unit without a registry entry kept running")
	}
}

func TestReconcileAdoptsMatchingUnitAndRestartsStaleOne(t *testing.T) {
	e := newSupEnv(t, nil)
	e.enable(t)
	pid := e.runner.adopt("traffic-alerts", Fingerprint(e.entry()))
	e.reconcile()
	if e.runner.startCalls != 0 || e.runner.startCount() != 0 || e.runner.pid("traffic-alerts") != pid {
		t.Fatalf("a running unit of the same build must be adopted untouched (start calls=%d starts=%d pid=%d want %d)", e.runner.startCalls, e.runner.startCount(), e.runner.pid("traffic-alerts"), pid)
	}
	if rt, ok := e.sup.Runtime("traffic-alerts"); !ok || rt.State != StateRunning || rt.PID != pid {
		t.Errorf("adopted runtime = %+v, %v", rt, ok)
	}

	stale := newSupEnv(t, nil)
	stale.enable(t)
	old := stale.runner.adopt("traffic-alerts", "an-older-build")
	stale.reconcile()
	if stale.runner.startCount() != 1 || stale.runner.pid("traffic-alerts") == old {
		t.Error("a unit from another build must be restarted")
	}
}

func TestReconcileDisablesCrashLoopingPlugin(t *testing.T) {
	e := newSupEnv(t, nil)
	e.enable(t)
	e.reconcile()
	e.runner.crash("traffic-alerts")
	e.reconcile()

	got := e.entry()
	if got.Enabled || !strings.Contains(got.DisabledReason, "start-limit-hit") {
		t.Fatalf("entry after a crash loop = %+v", got)
	}
	e.reconcile()
	if e.runner.running("traffic-alerts") {
		t.Error("a plugin disabled for crashing was restarted")
	}

	e.enable(t)
	e.reconcile()
	if !e.runner.running("traffic-alerts") || e.entry().DisabledReason != "" {
		t.Errorf("re-enabling must start it again and clear the reason: %+v", e.entry())
	}
}

func TestReconcileDisablesPluginThatCannotStart(t *testing.T) {
	arch := newSupEnv(t, nil, func(o *SupervisorOptions) { o.GOARCH = "arm64" })
	arch.enable(t)
	arch.reconcile()
	if got := arch.entry(); got.Enabled || !strings.Contains(got.DisabledReason, "architecture") {
		t.Errorf("a plugin without a build for this CPU: %+v", got)
	}

	changed := newSupEnv(t, nil)
	changed.enable(t)
	manifest := filepath.Join(changed.codeDir, "traffic-alerts", ManifestFile)
	if err := os.WriteFile(manifest, []byte(`{"id":"traffic-alerts"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	changed.reconcile()
	if got := changed.entry(); got.Enabled || !strings.Contains(got.DisabledReason, "changed") {
		t.Errorf("a plugin whose files changed after install: %+v", got)
	}
	if changed.runner.startCount() != 0 {
		t.Error("tampered plugin was started")
	}
}

func TestReconcileRetriesStartFailuresBeforeDisabling(t *testing.T) {
	e := newSupEnv(t, nil)
	e.enable(t)
	e.runner.startErr = errors.New("systemd-run exploded")

	for i := 1; i < maxStartFailures; i++ {
		e.reconcile()
		if got := e.entry(); !got.Enabled || got.DisabledReason != "" {
			t.Fatalf("disabled after %d failure(s), limit is %d: %+v", i, maxStartFailures, got)
		}
	}
	e.runner.startErr = nil
	e.reconcile()
	if !e.runner.running("traffic-alerts") {
		t.Fatal("a recovered start was not applied")
	}

	e.runner.startErr = errors.New("systemd-run exploded")
	e.runner.crashless(t, "traffic-alerts")
	for i := 1; i < maxStartFailures; i++ {
		e.reconcile()
	}
	if !e.entry().Enabled {
		t.Fatal("the failure counter must reset after a successful start")
	}
	e.reconcile()
	if got := e.entry(); got.Enabled || !strings.Contains(got.DisabledReason, "systemd-run exploded") {
		t.Errorf("after %d consecutive failures: %+v", maxStartFailures, got)
	}
}

// crashless removes the unit so the next reconcile has to start it again.
func (f *fakeRunner) crashless(t *testing.T, id string) {
	t.Helper()
	_ = f.Stop(context.Background(), id)
}

func TestReconcileWithUnavailableRunnerChangesNothing(t *testing.T) {
	e := newSupEnv(t, nil)
	e.enable(t)
	e.runner.unavailable = errors.New("this system was not booted with systemd")
	e.reconcile()

	if got := e.entry(); !got.Enabled || got.DisabledReason != "" {
		t.Errorf("an unavailable runner must not disable plugins: %+v", got)
	}
	if err := e.sup.Available(); err == nil || !strings.Contains(err.Error(), "systemd") {
		t.Errorf("Available = %v, want the runner's reason", err)
	}
	if err := e.sup.StopPlugin(context.Background(), "traffic-alerts"); err != nil {
		t.Errorf("StopPlugin without a runner must not block uninstall: %v", err)
	}
}

func TestRuntimeReportsHealthOverTheUnixSocket(t *testing.T) {
	sockDir, err := os.MkdirTemp("", "lucxh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })

	e := newSupEnv(t, nil, func(o *SupervisorOptions) { o.Health = nil })
	e.runner.socketDir = sockDir
	e.enable(t)

	e.reconcile()
	if rt, _ := e.sup.Runtime("traffic-alerts"); rt.Healthy || rt.HealthError == "" {
		t.Errorf("no listener must read as unhealthy with a reason: %+v", rt)
	}

	status := http.StatusOK
	var mu sync.Mutex
	ln, err := net.Listen("unix", filepath.Join(sockDir, "traffic-alerts.sock"))
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path != "/_lucx/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
	}), ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	e.reconcile()
	if rt, _ := e.sup.Runtime("traffic-alerts"); !rt.Healthy || rt.HealthError != "" {
		t.Errorf("a 200 from /_lucx/health must read as healthy: %+v", rt)
	}

	mu.Lock()
	status = http.StatusInternalServerError
	mu.Unlock()
	e.reconcile()
	if rt, _ := e.sup.Runtime("traffic-alerts"); rt.Healthy || !strings.Contains(rt.HealthError, "500") {
		t.Errorf("a 500 must read as unhealthy and say so: %+v", rt)
	}
}

func TestRestartRestartsEnabledPluginOnly(t *testing.T) {
	e := newSupEnv(t, nil)
	if err := e.sup.Restart(context.Background(), "traffic-alerts"); err == nil {
		t.Error("restarting a disabled plugin must fail")
	}
	e.enable(t)
	e.reconcile()
	before := e.runner.pid("traffic-alerts")
	if err := e.sup.Restart(context.Background(), "traffic-alerts"); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if after := e.runner.pid("traffic-alerts"); after == 0 || after == before {
		t.Errorf("pid %d -> %d: the process was not restarted", before, after)
	}
	if err := e.sup.Restart(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Restart of an unknown plugin: %v", err)
	}
}

func TestRunReconcilesOnKick(t *testing.T) {
	e := newSupEnv(t, nil, func(o *SupervisorOptions) { o.Interval = time.Hour })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.sup.Run(ctx); close(done) }()

	e.enable(t)
	e.sup.Kick()
	waitFor(t, "the kicked reconcile to start the plugin", func() bool { return e.runner.running("traffic-alerts") })

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if !e.runner.running("traffic-alerts") {
		t.Error("stopping the supervisor must leave plugin processes running")
	}
}

func TestStopPluginAndStopAllUnits(t *testing.T) {
	e := newSupEnv(t, nil)
	e.enable(t)
	e.reconcile()
	if err := e.sup.StopPlugin(context.Background(), "traffic-alerts"); err != nil {
		t.Fatal(err)
	}
	if e.runner.running("traffic-alerts") {
		t.Error("StopPlugin left the process running")
	}

	r := newFakeRunner()
	r.adopt("one-plugin", "x")
	r.adopt("two-plugin", "y")
	if err := StopAllUnits(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if r.running("one-plugin") || r.running("two-plugin") {
		t.Error("StopAllUnits left units running")
	}
}

func TestReconcileDeliversATokenThroughACredentialFile(t *testing.T) {
	secrets := t.TempDir()
	e := newSupEnv(t, nil)
	e.runner.secretsDir = secrets
	e.enable(t)
	e.reconcile()

	u := e.runner.units["traffic-alerts"]
	want := filepath.Join(secrets, "traffic-alerts")
	if u.spec.TokenFile != want {
		t.Fatalf("TokenFile = %q, want %q", u.spec.TokenFile, want)
	}
	raw, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := e.m.VerifyToken(string(raw))
	if !ok || got.ID != "traffic-alerts" {
		t.Fatalf("the delivered token does not verify: %q", raw)
	}
	st, _ := os.Stat(want)
	dir, _ := os.Stat(secrets)
	if st.Mode().Perm() != 0o400 || dir.Mode().Perm() != 0o700 {
		t.Errorf("token file %v in a %v directory; the secret must be root-only", st.Mode().Perm(), dir.Mode().Perm())
	}

	if err := e.m.SetEnabled("traffic-alerts", false); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	if _, err := os.Stat(want); !os.IsNotExist(err) {
		t.Error("the token file must be removed when the plugin stops")
	}
}

func TestReconcileWithoutASecretsDirStartsWithoutAToken(t *testing.T) {
	e := newSupEnv(t, nil)
	e.enable(t)
	e.reconcile()
	if u := e.runner.units["traffic-alerts"]; u == nil || u.spec.TokenFile != "" {
		t.Errorf("spec = %+v", u)
	}
}

func TestReconcilePassesThePanelAddress(t *testing.T) {
	e := newSupEnv(t, nil)
	e.m.cfg.PanelURL = func() string { return "https://10.0.0.5:2053/base" }
	e.enable(t)
	e.reconcile()
	u := e.runner.units["traffic-alerts"]
	if u.spec.PanelURL != "https://10.0.0.5:2053/base" || u.spec.PanelAddr != "10.0.0.5" {
		t.Errorf("spec = %+v", u.spec)
	}
}

func TestAChangedPanelAddressRestartsAdoptedPlugins(t *testing.T) {
	e := newSupEnv(t, nil)
	e.enable(t)
	e.m.cfg.PanelURL = func() string { return "http://127.0.0.1:2053" }
	e.reconcile()
	first := e.runner.pid("traffic-alerts")

	e.reconcile()
	if e.runner.pid("traffic-alerts") != first {
		t.Fatal("an unchanged address must not restart the plugin")
	}

	e.m.cfg.PanelURL = func() string { return "http://127.0.0.1:19055" }
	e.reconcile()
	if e.runner.pid("traffic-alerts") == first {
		t.Error("the panel address is baked into the unit environment; a new address needs a restart or the plugin keeps calling the old port")
	}
}

func TestWantsProcessFollowsTheRoleOfTheInstall(t *testing.T) {
	cases := []struct {
		name string
		e    Entry
		want bool
	}{
		{"local master plugin", Entry{Enabled: true, Type: TypeProcess, Scope: ScopeMaster}, true},
		{"local both plugin", Entry{Enabled: true, Type: TypeProcess, Scope: ScopeBoth}, true},
		{"local node plugin needs a master", Entry{Enabled: true, Type: TypeProcess, Scope: ScopeNode}, false},
		{"pushed node plugin", Entry{Enabled: true, Type: TypeProcess, Scope: ScopeNode, ManagedByMaster: true}, true},
		{"pushed both plugin", Entry{Enabled: true, Type: TypeHook, Scope: ScopeBoth, ManagedByMaster: true}, true},
		{"pushed master plugin never runs on a node", Entry{Enabled: true, Type: TypeProcess, Scope: ScopeMaster, ManagedByMaster: true}, false},
		{"disabled", Entry{Enabled: false, Type: TypeProcess, Scope: ScopeBoth}, false},
		{"interface plugin has no process", Entry{Enabled: true, Type: TypeUI, Scope: ScopeBoth}, false},
	}
	for _, tc := range cases {
		if got := wantsProcess(tc.e); got != tc.want {
			t.Errorf("%s: wantsProcess = %v, want %v", tc.name, got, tc.want)
		}
	}
}
