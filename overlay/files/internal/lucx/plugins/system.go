// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	systemConfigPath = "/etc/lucx-plugins/config"
	systemUnitDir    = "/etc/systemd/system"
	syncUnit         = "lucx-plugins-sync.service"
	applyUnit        = "lucx-plugins-apply.service"
	defaultStateDir  = "/var/lib/lucx-plugins"
	defaultRestartAt = "04:00"
)

var (
	// ErrSystemNotManaged means the plugin system was not put on this host by its installer.
	ErrSystemNotManaged = errors.New("the plugin system is not managed by the lucx-plugins installer on this host")
	// ErrSystemBusy means a check or an update is already running.
	ErrSystemBusy = errors.New("a check or an update of the plugin system is already running")
)

// SystemStatus is what the Plugins page shows about the plugin system itself: which build of it is
// installed and whether a newer one is waiting. It comes from the installer's state file.
type SystemStatus struct {
	Managed   bool       `json:"managed"`
	Reason    string     `json:"reason,omitempty"` // not-installed or units-missing, when not managed
	Upstream  string     `json:"upstream,omitempty"`
	Release   string     `json:"release,omitempty"`
	SwappedAt *time.Time `json:"swappedAt,omitempty"`
	CheckedAt *time.Time `json:"checkedAt,omitempty"`
	Pending   bool       `json:"pending"` // a newer build is ready and waits for the next start of the panel
	Busy      bool       `json:"busy"`    // a check or an update is running right now
	RestartAt string     `json:"restartAt,omitempty"`
	LastError string     `json:"lastError,omitempty"`
}

// systemState mirrors the installer's state.json; only the fields the page needs.
type systemState struct {
	Upstream  string    `json:"upstream"`
	Release   string    `json:"release"`
	SwappedAt time.Time `json:"swappedAt"`
	CheckedAt time.Time `json:"checkedAt"`
	Pending   bool      `json:"pending"`
	LastError string    `json:"lastError"`
}

// SystemControl reads that state and starts the installer's systemd units. Run is replaceable for tests.
type SystemControl struct {
	ConfigPath string
	UnitDir    string
	Run        func(ctx context.Context, name string, args ...string) ([]byte, error)
}

// NewSystemControl looks at the standard installer locations and runs real commands.
func NewSystemControl() *SystemControl {
	return &SystemControl{
		ConfigPath: systemConfigPath,
		UnitDir:    systemUnitDir,
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).CombinedOutput()
		},
	}
}

func readSystemConfig(path string) (stateDir, restartAt string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	stateDir, restartAt = defaultStateDir, defaultRestartAt
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.HasPrefix(line, "#") {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch strings.TrimSpace(key) {
		case "STATE_DIR":
			stateDir = value
		case "RESTART_AT":
			restartAt = value
		}
	}
	return stateDir, restartAt, sc.Err()
}

func (c *SystemControl) busy(ctx context.Context) bool {
	for _, unit := range []string{syncUnit, applyUnit} {
		// is-active exits non-zero for a unit that is not active; only its word matters.
		out, _ := c.Run(ctx, "systemctl", "is-active", unit)
		switch strings.TrimSpace(string(out)) {
		case "activating", "active", "reloading":
			return true
		}
	}
	return false
}

// Status describes the installed plugin system without touching the network.
func (c *SystemControl) Status(ctx context.Context) SystemStatus {
	stateDir, restartAt, err := readSystemConfig(c.ConfigPath)
	if err != nil {
		return SystemStatus{Reason: "not-installed"}
	}
	if _, err := os.Stat(filepath.Join(c.UnitDir, applyUnit)); err != nil {
		return SystemStatus{Reason: "units-missing"}
	}
	st := SystemStatus{Managed: true, RestartAt: restartAt}
	raw, err := os.ReadFile(filepath.Join(stateDir, "state.json"))
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		st.LastError = "the state file is unreadable: " + err.Error()
	default:
		var s systemState
		if err := json.Unmarshal(raw, &s); err != nil {
			st.LastError = "the state file is unreadable: " + err.Error()
			break
		}
		st.Upstream, st.Release, st.Pending, st.LastError = s.Upstream, s.Release, s.Pending, s.LastError
		if !s.SwappedAt.IsZero() {
			st.SwappedAt = &s.SwappedAt
		}
		if !s.CheckedAt.IsZero() {
			st.CheckedAt = &s.CheckedAt
		}
	}
	st.Busy = c.busy(ctx)
	return st
}

func (c *SystemControl) start(ctx context.Context, unit string) error {
	st := c.Status(ctx)
	if !st.Managed {
		return ErrSystemNotManaged
	}
	if st.Busy {
		return ErrSystemBusy
	}
	// --no-block: an update restarts the panel that is asking, so the answer must not wait for it.
	if out, err := c.Run(ctx, "systemctl", "start", "--no-block", unit); err != nil {
		return fmt.Errorf("cannot start %s: %w: %s", unit, err, string(bytes.TrimSpace(out)))
	}
	return nil
}

// Check starts a check for a newer build; it never restarts the panel.
func (c *SystemControl) Check(ctx context.Context) error { return c.start(ctx, syncUnit) }

// Update starts a check and, when a newer build is ready, the restart that installs it.
func (c *SystemControl) Update(ctx context.Context) error { return c.start(ctx, applyUnit) }
