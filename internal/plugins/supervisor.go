// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/AllIsA1/lucx-plugins/internal/logx"
)

const (
	defaultReconcileInterval = 10 * time.Second
	healthTimeout            = 2 * time.Second
	maxStartFailures         = 3
	healthPath               = "/_lucx/health"
)

// SupervisorOptions tunes a Supervisor; zero values select the defaults.
type SupervisorOptions struct {
	Interval        time.Duration
	GOARCH          string
	Health          func(ctx context.Context, socket string) error
	MaxProxyBody    int64
	PipelineTimeout time.Duration
	HookTimeout     time.Duration
	// SeamTimeout bounds one call of a plugin's seam; after SeamBreakerThreshold failures in a row the
	// plugin is left alone for SeamBreakerCooldown and the panel's own code runs.
	SeamTimeout          time.Duration
	SeamBreakerThreshold int
	SeamBreakerCooldown  time.Duration
}

// PluginRuntime is what the UI shows about a plugin's process.
type PluginRuntime struct {
	State       RunState `json:"state"`
	Healthy     bool     `json:"healthy"`
	HealthError string   `json:"healthError,omitempty"`
	PID         int      `json:"pid"`
	Restarts    int      `json:"restarts"`
	Detail      string   `json:"detail,omitempty"`

	EventsSent     int    `json:"eventsSent,omitempty"`
	EventsFailed   int    `json:"eventsFailed,omitempty"`
	EventsDropped  int    `json:"eventsDropped,omitempty"`
	PipelineErrors int    `json:"pipelineErrors,omitempty"`
	SeamCalls      int    `json:"seamCalls,omitempty"`
	SeamErrors     int    `json:"seamErrors,omitempty"`
	SeamSkipped    int    `json:"seamSkipped,omitempty"`
	LastError      string `json:"lastError,omitempty"`
}

// Supervisor keeps the running processes in line with the registry. It owns no
// process: stopping it leaves plugins running, a new Supervisor adopts them.
type Supervisor struct {
	mgr    *Manager
	runner Runner
	opt    SupervisorOptions
	kick   chan struct{}

	reconcileMu sync.Mutex

	mu        sync.RWMutex
	runtime   map[string]PluginRuntime
	failures  map[string]int
	hookTries map[string]int

	statsMu sync.Mutex
	stats   map[string]*pluginStats

	cacheMu    sync.Mutex
	patchCache map[string]cachedPatch

	seamMu     sync.Mutex
	seamStates map[string]*seamState

	queueMu sync.Mutex
	queues  map[string]chan eventJob
	closed  bool
	workers sync.WaitGroup
}

// NewSupervisor wires a Manager to a Runner and registers itself as the
// Manager's process controller.
func NewSupervisor(m *Manager, r Runner, opt SupervisorOptions) *Supervisor {
	if opt.Interval <= 0 {
		opt.Interval = defaultReconcileInterval
	}
	if opt.GOARCH == "" {
		opt.GOARCH = goArch()
	}
	if opt.Health == nil {
		opt.Health = unixHealth
	}
	if opt.MaxProxyBody <= 0 {
		opt.MaxProxyBody = defaultMaxProxyBody
	}
	if opt.HookTimeout <= 0 {
		opt.HookTimeout = defaultHookTimeout
	}
	if opt.SeamTimeout <= 0 {
		opt.SeamTimeout = defaultSeamTimeout
	}
	if opt.SeamBreakerThreshold <= 0 {
		opt.SeamBreakerThreshold = defaultSeamBreakerThreshold
	}
	if opt.SeamBreakerCooldown <= 0 {
		opt.SeamBreakerCooldown = defaultSeamBreakerCooldown
	}
	if opt.PipelineTimeout <= 0 {
		opt.PipelineTimeout = defaultPipelineTimeout
	}
	s := &Supervisor{
		mgr:       m,
		runner:    r,
		opt:       opt,
		kick:      make(chan struct{}, 1),
		runtime:   map[string]PluginRuntime{},
		failures:  map[string]int{},
		hookTries: map[string]int{},
		stats:     map[string]*pluginStats{},
		queues:    map[string]chan eventJob{},

		patchCache: map[string]cachedPatch{},
		seamStates: map[string]*seamState{},
	}
	m.SetProcessController(s)
	return s
}

// Available reports why processes cannot run on this system, or nil.
func (s *Supervisor) Available() error { return s.runner.Available() }

// Isolation reports what the runner enforces on this host.
func (s *Supervisor) Isolation() Isolation { return s.runner.Isolation() }

// Shutdown stops the processes of a runner whose plugins cannot outlive the
// panel; systemd units are left for the next Supervisor to adopt.
func (s *Supervisor) Shutdown(ctx context.Context) error {
	if all, ok := s.runner.(interface{ StopAll(context.Context) error }); ok {
		return all.StopAll(ctx)
	}
	return nil
}

// Paths exposes where the runner keeps a plugin's socket and data.
func (s *Supervisor) Paths(id string) (socket, data string) { return s.runner.Paths(id) }

// Kick asks Run for an immediate reconcile; it never blocks.
func (s *Supervisor) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// Run reconciles on a timer and on Kick until ctx ends.
func (s *Supervisor) Run(ctx context.Context) {
	ticker := time.NewTicker(s.opt.Interval)
	defer ticker.Stop()
	defer s.Close()
	s.Reconcile(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.kick:
		}
		s.Reconcile(ctx)
	}
}

// wantsProcess: an enabled process or hook plugin whose role (runsOnThisPanel)
// fits this panel.
func wantsProcess(e Entry) bool {
	return e.Enabled && (e.Type == TypeProcess || e.Type == TypeHook) && runsOnThisPanel(e)
}

// runsOnThisPanel is the role check: a plugin installed here is a master plugin, one a
// master pushed here is a node plugin.
func runsOnThisPanel(e Entry) bool {
	if e.ManagedByMaster {
		return e.Scope == ScopeNode || e.Scope == ScopeBoth
	}
	return e.Scope == ScopeMaster || e.Scope == ScopeBoth
}

// Reconcile drives actual processes toward the registry's desired state once.
func (s *Supervisor) Reconcile(ctx context.Context) {
	s.reconcileMu.Lock()
	defer s.reconcileMu.Unlock()
	if err := s.runner.Available(); err != nil {
		return
	}
	listed, err := s.runner.List(ctx)
	if err != nil {
		logx.Warning("plugins: cannot list plugin units:", err)
		return
	}
	actual := make(map[string]RunStatus, len(listed))
	for _, st := range listed {
		actual[st.ID] = st
	}
	desired := map[string]Entry{}
	for _, e := range s.mgr.List() {
		if wantsProcess(e) {
			desired[e.ID] = e
		}
	}
	for id := range actual {
		if _, ok := desired[id]; !ok {
			if err := s.runner.Stop(ctx, id); err != nil {
				logx.Warningf("plugins: cannot stop unit of %s: %v", id, err)
			}
			s.removeToken(id)
		}
	}

	next := map[string]PluginRuntime{}
	ids := make([]string, 0, len(desired))
	for id := range desired {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if rt, ok := s.reconcileOne(ctx, desired[id], actual[id]); ok {
			next[id] = rt
		}
	}
	s.mu.Lock()
	s.runtime = next
	s.mu.Unlock()
}

func (s *Supervisor) reconcileOne(ctx context.Context, e Entry, st RunStatus) (PluginRuntime, bool) {
	switch st.State {
	case StateFailed:
		reason := st.Detail
		if reason == "" {
			reason = "failed"
		}
		s.disable(ctx, e.ID, fmt.Sprintf("the process keeps failing: %s after %d restarts", reason, st.Restarts))
		return PluginRuntime{}, false
	case StateStopping:
		return PluginRuntime{State: StateStopping}, true
	}

	spec, err := s.spec(e)
	if err != nil {
		s.disable(ctx, e.ID, "cannot start: "+err.Error())
		return PluginRuntime{}, false
	}
	alive := st.State == StateRunning || st.State == StateStarting
	if !alive || st.Fingerprint != spec.Fingerprint {
		if err := s.writeToken(e, &spec); err != nil {
			return s.startFailed(ctx, e.ID, fmt.Errorf("cannot store the plugin token: %w", err))
		}
		if err := s.runner.Start(ctx, spec); err != nil {
			return s.startFailed(ctx, e.ID, err)
		}
		s.mu.Lock()
		delete(s.failures, e.ID)
		s.mu.Unlock()
		fresh, err := s.runner.Status(ctx, e.ID)
		if err != nil {
			return PluginRuntime{State: StateStarting}, true
		}
		st = fresh
	}
	rt := PluginRuntime{State: st.State, PID: st.PID, Restarts: st.Restarts, Detail: st.Detail}
	if st.State == StateRunning {
		hctx, cancel := context.WithTimeout(ctx, healthTimeout)
		if err := s.opt.Health(hctx, spec.SocketPath); err != nil {
			rt.HealthError = err.Error()
		} else {
			rt.Healthy = true
		}
		cancel()
		if rt.Healthy {
			s.installHook(ctx, e, spec)
		}
	}
	return rt, true
}

func (s *Supervisor) startFailed(ctx context.Context, id string, err error) (PluginRuntime, bool) {
	s.mu.Lock()
	s.failures[id]++
	n := s.failures[id]
	s.mu.Unlock()
	logx.Warningf("plugins: start of %s failed (%d/%d): %v", id, n, maxStartFailures, err)
	if n >= maxStartFailures {
		s.disable(ctx, id, fmt.Sprintf("cannot start: %v", err))
		return PluginRuntime{}, false
	}
	return PluginRuntime{State: StateStopped, Detail: err.Error()}, true
}

func (s *Supervisor) disable(ctx context.Context, id, reason string) {
	logx.Warningf("plugins: disabling %s: %s", id, reason)
	if err := s.mgr.markDisabled(id, reason); err != nil {
		logx.Warningf("plugins: cannot record why %s was disabled: %v", id, err)
	}
	_ = s.runner.Stop(ctx, id)
	s.removeToken(id)
	s.mu.Lock()
	delete(s.failures, id)
	s.mu.Unlock()
}

// loadManifest reads the installed manifest and refuses one that changed since installation.
func (s *Supervisor) loadManifest(e Entry) (*Manifest, error) {
	raw, err := os.ReadFile(filepath.Join(s.mgr.cfg.CodeDir, e.ID, ManifestFile))
	if err != nil {
		return nil, fmt.Errorf("cannot read the plugin manifest: %w", err)
	}
	if e.ManifestSHA256 != "" {
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != e.ManifestSHA256 {
			return nil, errors.New("plugin files changed after installation")
		}
	}
	return ParseManifest(raw)
}

func (s *Supervisor) spec(e Entry) (ProcessSpec, error) {
	m, err := s.loadManifest(e)
	if err != nil {
		return ProcessSpec{}, err
	}
	socket, data := s.runner.Paths(e.ID)
	spec, err := ResolveSpec(e, m, s.mgr.cfg.CodeDir, socket, data, s.opt.GOARCH)
	if err != nil {
		return ProcessSpec{}, err
	}
	if s.mgr.cfg.PanelURL != nil {
		spec.PanelURL = s.mgr.cfg.PanelURL()
		spec.PanelAddr = panelAddrOf(spec.PanelURL)
		if spec.PanelURL != "" {
			sum := sha256.Sum256([]byte(spec.PanelURL))
			spec.Fingerprint += "." + hex.EncodeToString(sum[:4])
		}
	}
	return spec, nil
}

// secretPath is the credential file systemd loads into the plugin's unit.
func (s *Supervisor) secretPath(id string) string {
	dir := s.runner.SecretsDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, id)
}

// writeToken stores the plugin's API token where only root can read it.
func (s *Supervisor) writeToken(e Entry, spec *ProcessSpec) error {
	path := s.secretPath(e.ID)
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tok-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(s.mgr.IssueToken(e)); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o400); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	spec.TokenFile = path
	return nil
}

func (s *Supervisor) removeToken(id string) {
	if path := s.secretPath(id); path != "" {
		_ = os.Remove(path)
	}
}

// Runtime reports the process state of a process-type plugin.
func (s *Supervisor) Runtime(id string) (PluginRuntime, bool) {
	e, ok := s.mgr.reg.Get(id)
	if !ok || (e.Type != TypeProcess && e.Type != TypeHook) {
		return PluginRuntime{}, false
	}
	s.mu.RLock()
	rt, known := s.runtime[id]
	s.mu.RUnlock()
	if !known {
		rt = PluginRuntime{State: StateStopped}
	}
	s.statsMu.Lock()
	if st := s.stats[id]; st != nil {
		rt.EventsSent, rt.EventsFailed, rt.EventsDropped = st.sent, st.failed, st.dropped
		rt.PipelineErrors, rt.LastError = st.pipelineErrors, st.lastError
		rt.SeamCalls, rt.SeamErrors, rt.SeamSkipped = st.seamCalls, st.seamErrors, st.seamSkipped
	}
	s.statsMu.Unlock()
	return rt, true
}

// Logs returns the last lines the plugin process wrote.
func (s *Supervisor) Logs(ctx context.Context, id string, lines int) ([]string, error) {
	if _, ok := s.mgr.reg.Get(id); !ok {
		return nil, ErrNotFound
	}
	if err := s.runner.Available(); err != nil {
		return nil, err
	}
	return s.runner.Logs(ctx, id, lines)
}

// Restart stops an enabled plugin and starts it again right away.
func (s *Supervisor) Restart(ctx context.Context, id string) error {
	e, ok := s.mgr.reg.Get(id)
	if !ok {
		return ErrNotFound
	}
	if !e.Enabled {
		return errors.New("the plugin is disabled; enable it first")
	}
	if err := s.runner.Available(); err != nil {
		return err
	}
	if err := s.runner.Stop(ctx, id); err != nil {
		return err
	}
	s.Reconcile(ctx)
	return nil
}

// StopPlugin stops one plugin's process; without a runner there is nothing to stop.
func (s *Supervisor) StopPlugin(ctx context.Context, id string) error {
	if s.runner.Available() != nil {
		return nil
	}
	if err := s.runner.Stop(ctx, id); err != nil {
		return err
	}
	s.removeToken(id)
	s.mu.Lock()
	delete(s.runtime, id)
	delete(s.failures, id)
	s.mu.Unlock()
	return nil
}

// StopAllUnits stops every plugin unit the runner knows; safe mode uses it.
func StopAllUnits(ctx context.Context, r Runner) error {
	if err := r.Available(); err != nil {
		return nil
	}
	listed, err := r.List(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, st := range listed {
		errs = append(errs, r.Stop(ctx, st.ID))
	}
	return errors.Join(errs...)
}

// unixHealth asks the plugin's own socket whether it is ready to serve.
func unixHealth(ctx context.Context, socket string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://plugin"+healthPath, nil)
	if err != nil {
		return err
	}
	resp, err := newSocketClient(socket, healthTimeout).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check returned HTTP %d", resp.StatusCode)
	}
	return nil
}
