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
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// RunState is the coarse lifecycle state of a plugin process.
type RunState string

const (
	StateStopped  RunState = "stopped"
	StateStarting RunState = "starting"
	StateRunning  RunState = "running"
	StateStopping RunState = "stopping"
	StateFailed   RunState = "failed"
)

// ResourceLimits bound a plugin process; zero values select the defaults.
type ResourceLimits struct {
	MemoryMax       string
	TasksMax        int
	CPUQuotaPercent int
}

// DefaultResourceLimits suit a small helper process.
var DefaultResourceLimits = ResourceLimits{MemoryMax: "256M", TasksMax: 64, CPUQuotaPercent: 50}

// ProcessSpec is everything a Runner needs to start one plugin process.
type ProcessSpec struct {
	ID          string
	Fingerprint string
	CodeDir     string
	Command     string
	Args        []string
	SocketPath  string
	DataDir     string
	Env         map[string]string
	Permissions []string
	Hooks       HookSpec
	Limits      ResourceLimits
	TokenFile   string
	PanelURL    string
	PanelAddr   string
}

// RunStatus is the observed state of one plugin process.
type RunStatus struct {
	ID          string   `json:"id"`
	State       RunState `json:"state"`
	Fingerprint string   `json:"fingerprint"`
	PID         int      `json:"pid"`
	Restarts    int      `json:"restarts"`
	Detail      string   `json:"detail"`
}

// IsolationLevel summarises how well a runner confines a plugin process.
type IsolationLevel string

const (
	IsolationStrong  IsolationLevel = "strong"
	IsolationPartial IsolationLevel = "partial"
	IsolationNone    IsolationLevel = "none"
)

// Isolation states what a runner can enforce on this host, so the panel never
// promises more than it delivers (the network permission needs Network).
type Isolation struct {
	Runner     string         `json:"runner"`
	Level      IsolationLevel `json:"level"`
	Network    bool           `json:"network"`
	Filesystem bool           `json:"filesystem"`
	User       bool           `json:"user"`
	Notes      []string       `json:"notes,omitempty"`
}

// NewIsolation derives the level from the individual guarantees.
func NewIsolation(runner string, network, filesystem, user bool, notes ...string) Isolation {
	level := IsolationPartial
	switch {
	case network && filesystem && user:
		level = IsolationStrong
	case !network && !filesystem && !user:
		level = IsolationNone
	}
	return Isolation{Runner: runner, Level: level, Network: network, Filesystem: filesystem, User: user, Notes: notes}
}

// Runner starts and supervises plugin processes. Implementations are
// idempotent: Start with the running fingerprint changes nothing.
type Runner interface {
	Available() error
	Isolation() Isolation
	Paths(id string) (socket, data string)
	SecretsDir() string
	Start(ctx context.Context, spec ProcessSpec) error
	Stop(ctx context.Context, id string) error
	Status(ctx context.Context, id string) (RunStatus, error)
	List(ctx context.Context) ([]RunStatus, error)
	Logs(ctx context.Context, id string, lines int) ([]string, error)
}

// Fingerprint identifies the exact plugin build a process was started from.
func Fingerprint(e Entry) string {
	sum := e.ManifestSHA256
	if len(sum) > 12 {
		sum = sum[:12]
	}
	return e.Commit + "." + sum
}

// ResolveSpec turns an installed plugin into a ProcessSpec: placeholders are
// substituted and the executable is checked to be a real file inside codeDir.
func ResolveSpec(e Entry, m *Manifest, codeDir, socket, data, goarch string) (ProcessSpec, error) {
	if m.Run == nil {
		return ProcessSpec{}, errors.New("plugin has no run section")
	}
	pluginDir := filepath.Join(codeDir, e.ID)
	subst := func(v string) string {
		return strings.NewReplacer("${arch}", goarch, "${socket}", socket, "${data}", data).Replace(v)
	}
	command := filepath.Join(pluginDir, filepath.FromSlash(subst(m.Run.Command)))
	if err := checkExecutable(pluginDir, command); err != nil {
		return ProcessSpec{}, err
	}
	args := make([]string, len(m.Run.Args))
	for i, a := range m.Run.Args {
		args[i] = subst(a)
	}
	return ProcessSpec{
		ID:          e.ID,
		Fingerprint: Fingerprint(e),
		CodeDir:     pluginDir,
		Command:     command,
		Args:        args,
		SocketPath:  socket,
		DataDir:     data,
		Permissions: normalizePermissions(e.Permissions),
		Hooks:       m.Hooks,
		Limits:      DefaultResourceLimits,
	}, nil
}

func checkExecutable(pluginDir, command string) error {
	if !filepath.IsLocal(strings.TrimPrefix(command, pluginDir+string(filepath.Separator))) {
		return fmt.Errorf("command %q is outside the plugin directory", command)
	}
	// Lstat so a symlink pointing outside the plugin is refused, not followed.
	st, err := os.Lstat(command)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("plugin binary %q not found (does it support this architecture?)", command)
	}
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("plugin binary %q is not a regular file", command)
	}
	if st.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("plugin binary %q is not executable", command)
	}
	return nil
}

func goArch() string { return runtime.GOARCH }

type unavailableRunner struct{ reason error }

// NewUnavailableRunner is a Runner for systems that cannot run plugin
// processes; every call reports reason, so plugins stay installable but idle.
func NewUnavailableRunner(reason string) Runner {
	return unavailableRunner{reason: errors.New(reason)}
}

func (r unavailableRunner) SecretsDir() string            { return "" }
func (r unavailableRunner) Available() error              { return r.reason }
func (r unavailableRunner) Isolation() Isolation          { return NewIsolation("none", false, false, false) }
func (r unavailableRunner) Paths(string) (string, string) { return "", "" }
func (r unavailableRunner) Start(context.Context, ProcessSpec) error {
	return r.reason
}
func (r unavailableRunner) Stop(context.Context, string) error { return r.reason }
func (r unavailableRunner) Status(context.Context, string) (RunStatus, error) {
	return RunStatus{}, r.reason
}
func (r unavailableRunner) List(context.Context) ([]RunStatus, error) { return nil, r.reason }
func (r unavailableRunner) Logs(context.Context, string, int) ([]string, error) {
	return nil, r.reason
}
