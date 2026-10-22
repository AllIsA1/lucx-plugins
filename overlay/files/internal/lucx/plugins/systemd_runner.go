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
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	systemRuntimeRoot = "/run/x-ui-plugins"
	systemStateRoot   = "/var/lib/x-ui-plugins"
	systemSecretsRoot = "/run/x-ui-plugins-secrets"
	maxLogLines       = 1000
)

// SystemdRunner runs each plugin as a transient systemd service. The user
// variant exists for tests: same code paths, unprivileged manager.
type SystemdRunner struct {
	user        bool
	runtimeRoot string
	stateRoot   string

	restartSec    string
	initOnce      sync.Once
	initErr       error
	sysCallFilter bool
}

// NewSystemdRunner returns a runner backed by the system manager.
func NewSystemdRunner() *SystemdRunner {
	return &SystemdRunner{runtimeRoot: systemRuntimeRoot, stateRoot: systemStateRoot}
}

func newUserSystemdRunner(runtimeRoot, stateRoot string) *SystemdRunner {
	return &SystemdRunner{user: true, runtimeRoot: runtimeRoot, stateRoot: stateRoot}
}

func (r *SystemdRunner) init() error {
	r.initOnce.Do(func() {
		for _, tool := range []string{"systemd-run", "systemctl"} {
			if _, err := exec.LookPath(tool); err != nil {
				r.initErr = fmt.Errorf("%s not found: plugin processes need systemd (or set XUI_PLUGINS_RUNNER=process)", tool)
				return
			}
		}
		if !r.user {
			if st, err := os.Stat("/run/systemd/system"); err != nil || !st.IsDir() {
				r.initErr = errors.New("this system was not booted with systemd (or set XUI_PLUGINS_RUNNER=process)")
				return
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "systemctl", "--version").Output()
		if err != nil {
			r.initErr = fmt.Errorf("systemctl --version: %w", err)
			return
		}
		version, err := parseSystemdVersion(string(out))
		if err != nil {
			r.initErr = err
			return
		}
		r.sysCallFilter = version >= systemdFirst
	})
	return r.initErr
}

// Available reports why plugin processes cannot run here, or nil.
func (r *SystemdRunner) Available() error { return r.init() }

// Isolation reports the sandbox; the user manager has no DynamicUser or IP filter.
func (r *SystemdRunner) Isolation() Isolation {
	if r.user {
		return NewIsolation("systemd-user", false, true, false, "user systemd manager: no per-plugin user and no network filter")
	}
	return NewIsolation("systemd", true, true, true)
}

// Paths returns the socket and data locations systemd will create for id.
func (r *SystemdRunner) Paths(id string) (socket, data string) {
	return filepath.Join(r.runtimeRoot, id, "plugin.sock"), filepath.Join(r.stateRoot, id)
}

// SecretsDir is where the panel drops per-plugin credential files for systemd to load.
func (r *SystemdRunner) SecretsDir() string {
	if r.user {
		return filepath.Join(filepath.Dir(r.runtimeRoot), "x-ui-plugins-secrets")
	}
	return systemSecretsRoot
}

func (r *SystemdRunner) options() systemdOptions {
	return systemdOptions{User: r.user, SystemCallFilter: r.sysCallFilter, RestartSec: r.restartSec}
}

func (r *SystemdRunner) run(ctx context.Context, tool string, args ...string) ([]byte, error) {
	if r.user {
		args = append([]string{"--user"}, args...)
	}
	out, err := exec.CommandContext(ctx, tool, args...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w: %s", tool, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// Start launches the plugin unless the same build is already running.
func (r *SystemdRunner) Start(ctx context.Context, spec ProcessSpec) error {
	if err := r.init(); err != nil {
		return err
	}
	if !idPattern.MatchString(spec.ID) {
		return fmt.Errorf("invalid plugin id %q", spec.ID)
	}
	st, err := r.Status(ctx, spec.ID)
	if err != nil {
		return err
	}
	if (st.State == StateRunning || st.State == StateStarting) && st.Fingerprint == spec.Fingerprint {
		return nil
	}
	if st.State != StateStopped {
		if err := r.Stop(ctx, spec.ID); err != nil {
			return err
		}
	}
	args := systemdRunArgs(spec, r.options())
	if r.user {
		args = args[1:] // run() adds --user itself
	}
	_, err = r.run(ctx, "systemd-run", args...)
	return err
}

// Stop stops the unit and clears a failed state; a missing unit is not an error.
func (r *SystemdRunner) Stop(ctx context.Context, id string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("invalid plugin id %q", id)
	}
	if out, err := r.run(ctx, "systemctl", "stop", unitName(id)); err != nil && !strings.Contains(string(out), "not loaded") {
		return err
	}
	_, _ = r.run(ctx, "systemctl", "reset-failed", unitName(id))
	return nil
}

const showProps = "Description,ActiveState,SubState,MainPID,NRestarts,Result"

// Status reads the live state of one plugin unit.
func (r *SystemdRunner) Status(ctx context.Context, id string) (RunStatus, error) {
	if !idPattern.MatchString(id) {
		return RunStatus{}, fmt.Errorf("invalid plugin id %q", id)
	}
	out, err := r.run(ctx, "systemctl", "show", "-p", showProps, unitName(id))
	if err != nil {
		return RunStatus{}, err
	}
	blocks := parseShowBlocks(string(out))
	if len(blocks) != 1 {
		return RunStatus{}, fmt.Errorf("unexpected systemctl show output for %s", id)
	}
	return statusFromShow(id, blocks[0]), nil
}

// List returns every plugin unit systemd currently knows, whatever its state.
func (r *SystemdRunner) List(ctx context.Context) ([]RunStatus, error) {
	if err := r.init(); err != nil {
		return nil, err
	}
	out, err := r.run(ctx, "systemctl", "list-units", "lucx-plugin-*.service", "--all", "--plain", "--no-legend", "--no-pager")
	if err != nil {
		return nil, err
	}
	var list []RunStatus
	for _, id := range parseListedIDs(string(out)) {
		st, err := r.Status(ctx, id)
		if err != nil {
			return nil, err
		}
		list = append(list, st)
	}
	return list, nil
}

// Logs returns the last lines the plugin wrote to its journal.
func (r *SystemdRunner) Logs(ctx context.Context, id string, lines int) ([]string, error) {
	if !idPattern.MatchString(id) {
		return nil, fmt.Errorf("invalid plugin id %q", id)
	}
	lines = min(max(lines, 1), maxLogLines)
	out, err := r.run(ctx, "journalctl", "-u", unitName(id), "-n", strconv.Itoa(lines), "--no-pager", "-o", "cat")
	if err != nil {
		return nil, err
	}
	text := strings.TrimRight(string(out), "\n")
	if text == "" {
		return nil, nil
	}
	return strings.Split(text, "\n"), nil
}
