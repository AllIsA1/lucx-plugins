// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"
)

const (
	defaultRestartDelay = 5 * time.Second
	defaultMaxRestarts  = 5
	defaultRestartSpan  = 300 * time.Second
	defaultStopGrace    = 10 * time.Second
	pipeWaitDelay       = 2 * time.Second
	defaultSearchPath   = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
)

// processOptions place a ProcessRunner on disk and tune its restart policy;
// zero values select the defaults that mirror the systemd unit.
type processOptions struct {
	RuntimeRoot  string
	StateRoot    string
	RestartDelay time.Duration
	MaxRestarts  int
	RestartSpan  time.Duration
	StopGrace    time.Duration
	Sandbox      processSandbox
	Launcher     string
}

// processSandbox is the platform part of a ProcessRunner: how the child is
// confined and signalled, and what that promises.
type processSandbox interface {
	Check() error
	Isolation() Isolation
	Own(spec ProcessSpec, paths ownedPaths) error
	Prepare(cmd *exec.Cmd, spec ProcessSpec) error
	Signal(pid int, sig syscall.Signal)
}

// ownedPaths are the files and directories a plugin must be able to use.
type ownedPaths struct{ Run, Data, Token string }

// ProcessRunner runs each plugin as a child of the panel, for hosts without
// systemd such as the Docker image. Plugins die with the panel; the
// supervisor starts them again on the next boot.
type ProcessRunner struct {
	opt processOptions

	mu    sync.Mutex
	procs map[string]*childProc
	logs  map[string]*lineRing
}

type childProc struct {
	spec ProcessSpec
	logs *lineRing

	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}

	mu       sync.Mutex
	state    RunState
	pid      int
	restarts int
	detail   string
}

// newHostProcessRunner places a ProcessRunner under the panel's data root; the
// sockets and tokens live in /run (XUI_PLUGINS_RUNTIME_DIR), not persistent in a container.
func newHostProcessRunner(cfg Config) *ProcessRunner {
	return NewProcessRunner(processOptions{RuntimeRoot: envOr("XUI_PLUGINS_RUNTIME_DIR", "/run/x-ui-plugins"), StateRoot: cfg.DataDir})
}

// NewProcessRunner builds a runner; Available reports whether this host can use it.
func NewProcessRunner(opt processOptions) *ProcessRunner {
	if opt.RestartDelay == 0 {
		opt.RestartDelay = defaultRestartDelay
	}
	if opt.RestartSpan == 0 {
		opt.RestartSpan = defaultRestartSpan
	}
	if opt.StopGrace == 0 {
		opt.StopGrace = defaultStopGrace
	}
	if opt.MaxRestarts == 0 {
		opt.MaxRestarts = defaultMaxRestarts
	}
	if opt.Sandbox == nil {
		opt.Sandbox = newPlatformSandbox(opt)
	}
	return &ProcessRunner{opt: opt, procs: map[string]*childProc{}, logs: map[string]*lineRing{}}
}

func (r *ProcessRunner) Available() error { return r.opt.Sandbox.Check() }

func (r *ProcessRunner) Isolation() Isolation { return r.opt.Sandbox.Isolation() }

// SecretsDir holds the token files the supervisor writes before Start.
func (r *ProcessRunner) SecretsDir() string { return filepath.Join(r.opt.RuntimeRoot, ".secrets") }

func (r *ProcessRunner) Paths(id string) (socket, data string) {
	return filepath.Join(r.opt.RuntimeRoot, id, "plugin.sock"), filepath.Join(r.opt.StateRoot, id)
}

func (r *ProcessRunner) credentialsDir(id string) string {
	return filepath.Join(r.opt.RuntimeRoot, ".credentials", id)
}

// Start launches the plugin unless the same build is already running.
func (r *ProcessRunner) Start(ctx context.Context, spec ProcessSpec) error {
	if !idPattern.MatchString(spec.ID) {
		return fmt.Errorf("invalid plugin id %q", spec.ID)
	}
	if err := r.Available(); err != nil {
		return err
	}
	r.mu.Lock()
	cur := r.procs[spec.ID]
	r.mu.Unlock()
	if cur != nil {
		st := cur.snapshot()
		if (st.State == StateRunning || st.State == StateStarting) && st.Fingerprint == spec.Fingerprint {
			return nil
		}
		if err := r.Stop(ctx, spec.ID); err != nil {
			return err
		}
	}
	if err := r.prepareDirs(spec); err != nil {
		return err
	}
	p := &childProc{spec: spec, stop: make(chan struct{}), done: make(chan struct{}), state: StateStarting, logs: r.ringFor(spec.ID)}
	started := make(chan error, 1)
	r.mu.Lock()
	r.procs[spec.ID] = p
	r.mu.Unlock()
	go r.supervise(p, started)
	select {
	case err := <-started:
		if err != nil {
			r.forget(p)
		}
		return err
	case <-ctx.Done():
		p.stopOnce.Do(func() { close(p.stop) })
		return ctx.Err()
	}
}

func (r *ProcessRunner) ringFor(id string) *lineRing {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.logs[id] == nil {
		r.logs[id] = &lineRing{}
	}
	return r.logs[id]
}

func (r *ProcessRunner) forget(p *childProc) {
	<-p.done
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.procs[p.spec.ID] == p {
		delete(r.procs, p.spec.ID)
	}
}

func (r *ProcessRunner) prepareDirs(spec ProcessSpec) error {
	creds := r.credentialsDir(spec.ID)
	// Shared directories are searchable but not writable by plugins; only a plugin's own run and data directories are.
	for _, d := range []struct {
		path string
		mode os.FileMode
	}{
		{r.opt.RuntimeRoot, 0o711},
		{r.opt.StateRoot, 0o711},
		{filepath.Dir(creds), 0o711},
		{creds, 0o755},
		{filepath.Dir(spec.SocketPath), 0o700},
		{spec.DataDir, 0o700},
	} {
		if err := os.MkdirAll(d.path, d.mode); err != nil {
			return err
		}
		if err := os.Chmod(d.path, d.mode); err != nil {
			return err
		}
	}
	if err := removeIfExists(spec.SocketPath); err != nil {
		return err
	}
	owned := ownedPaths{Run: filepath.Dir(spec.SocketPath), Data: spec.DataDir}
	if spec.TokenFile != "" {
		raw, err := os.ReadFile(spec.TokenFile)
		if err != nil {
			return fmt.Errorf("cannot read the plugin token: %w", err)
		}
		owned.Token = filepath.Join(creds, tokenFileName)
		if err := writeSecretFile(owned.Token, raw, 0o400); err != nil {
			return err
		}
	} else if err := removeIfExists(filepath.Join(creds, tokenFileName)); err != nil {
		return err
	}
	return r.opt.Sandbox.Own(spec, owned)
}

func writeSecretFile(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (r *ProcessRunner) command(p *childProc) (*exec.Cmd, error) {
	spec := p.spec
	cmd := exec.CommandContext(context.Background(), spec.Command, spec.Args...)
	cmd.Dir = spec.CodeDir
	env := pluginEnv(spec)
	env["CREDENTIALS_DIRECTORY"] = r.credentialsDir(spec.ID)
	env["PATH"] = defaultSearchPath
	env["HOME"] = spec.DataDir
	env["STATE_DIRECTORY"] = spec.DataDir
	env["TMPDIR"] = spec.DataDir
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdout, cmd.Stderr = p.logs, p.logs
	cmd.WaitDelay = pipeWaitDelay
	if err := r.opt.Sandbox.Prepare(cmd, spec); err != nil {
		return nil, err
	}
	return cmd, nil
}

// supervise owns the child for its whole life. It stays on one OS thread
// because Pdeathsig fires when the thread that forked the child exits.
func (r *ProcessRunner) supervise(p *childProc, started chan<- error) {
	runtime.LockOSThread()
	defer close(p.done)
	var begun []time.Time
	for first := true; ; first = false {
		cmd, err := r.command(p)
		if err == nil {
			err = cmd.Start()
		}
		if err != nil {
			p.set(StateFailed, 0, "cannot start: "+err.Error())
			if first {
				started <- err
			}
			return
		}
		begun = append(begun, time.Now())
		p.set(StateRunning, cmd.Process.Pid, "")
		if first {
			started <- nil
		}
		exited := make(chan error, 1)
		go func() { exited <- cmd.Wait() }()
		select {
		case err := <-exited:
			r.opt.Sandbox.Signal(cmd.Process.Pid, syscall.SIGKILL)
			detail := exitDetail(err)
			if err == nil {
				p.set(StateStopped, 0, detail)
				return
			}
			begun = recent(begun, r.opt.RestartSpan)
			if len(begun) > r.opt.MaxRestarts {
				p.set(StateFailed, 0, detail+"; restart limit reached")
				return
			}
			p.restarted(detail)
			select {
			case <-time.After(r.opt.RestartDelay):
			case <-p.stop:
				p.set(StateStopped, 0, "")
				return
			}
		case <-p.stop:
			p.set(StateStopping, cmd.Process.Pid, "")
			_ = r.terminate(cmd, exited)
			p.set(StateStopped, 0, "")
			return
		}
	}
}

func (r *ProcessRunner) terminate(cmd *exec.Cmd, exited <-chan error) error {
	pid := cmd.Process.Pid
	r.opt.Sandbox.Signal(pid, syscall.SIGTERM)
	var err error
	select {
	case err = <-exited:
	case <-time.After(r.opt.StopGrace):
		r.opt.Sandbox.Signal(pid, syscall.SIGKILL)
		err = <-exited
	}
	r.opt.Sandbox.Signal(pid, syscall.SIGKILL)
	return err
}

func recent(times []time.Time, span time.Duration) []time.Time {
	cut := time.Now().Add(-span)
	for len(times) > 0 && times[0].Before(cut) {
		times = times[1:]
	}
	return times
}

func exitDetail(err error) string {
	var ee *exec.ExitError
	switch {
	case err == nil:
		return "exited with status 0"
	case errors.As(err, &ee):
		return "exited with " + ee.String()
	default:
		return "exited: " + err.Error()
	}
}

func (p *childProc) set(state RunState, pid int, detail string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.state, p.pid, p.detail = state, pid, detail
}

func (p *childProc) restarted(detail string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.restarts++
	p.state, p.pid, p.detail = StateStarting, 0, detail
}

func (p *childProc) snapshot() RunStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return RunStatus{ID: p.spec.ID, State: p.state, Fingerprint: p.spec.Fingerprint, PID: p.pid, Restarts: p.restarts, Detail: p.detail}
}

// Stop ends the plugin and everything it spawned; an unknown plugin is not an error.
func (r *ProcessRunner) Stop(ctx context.Context, id string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("invalid plugin id %q", id)
	}
	r.mu.Lock()
	p := r.procs[id]
	r.mu.Unlock()
	if p == nil {
		return nil
	}
	p.stopOnce.Do(func() { close(p.stop) })
	select {
	case <-p.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	r.mu.Lock()
	if r.procs[id] == p {
		delete(r.procs, id)
	}
	r.mu.Unlock()
	if err := os.RemoveAll(r.credentialsDir(id)); err != nil {
		return err
	}
	return removeIfExists(p.spec.SocketPath)
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// StopAll stops every plugin this runner started.
func (r *ProcessRunner) StopAll(ctx context.Context) error {
	st, _ := r.List(ctx)
	var errs []error
	for _, s := range st {
		errs = append(errs, r.Stop(ctx, s.ID))
	}
	return errors.Join(errs...)
}

func (r *ProcessRunner) Status(_ context.Context, id string) (RunStatus, error) {
	if !idPattern.MatchString(id) {
		return RunStatus{}, fmt.Errorf("invalid plugin id %q", id)
	}
	r.mu.Lock()
	p := r.procs[id]
	r.mu.Unlock()
	if p == nil {
		return RunStatus{ID: id, State: StateStopped}, nil
	}
	return p.snapshot(), nil
}

func (r *ProcessRunner) List(context.Context) ([]RunStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := make([]RunStatus, 0, len(r.procs))
	for _, p := range r.procs {
		list = append(list, p.snapshot())
	}
	return list, nil
}

// Logs returns the last lines the plugin wrote since the panel started.
func (r *ProcessRunner) Logs(_ context.Context, id string, lines int) ([]string, error) {
	if !idPattern.MatchString(id) {
		return nil, fmt.Errorf("invalid plugin id %q", id)
	}
	r.mu.Lock()
	ring := r.logs[id]
	r.mu.Unlock()
	if ring == nil {
		return nil, nil
	}
	return ring.last(min(max(lines, 1), maxLogLines)), nil
}
