// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

// Package panelsync lets the plugin host do what "lucx-plugins update" does: look for a newer plugin build of the
// panel and install it. The work is lucx-plugins-sync's; the host only starts it and reads the state it leaves.
//
// The host runs in a sandbox that cannot write the panel's binary or restart the panel, so the update is started
// as a transient systemd unit, outside that sandbox. A check only downloads into the sync tool's own state directory
// and runs as a plain child.
package panelsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Status is what the Plugins page shows of the panel's plugin build.
type Status struct {
	Managed   bool      `json:"managed"`   // the sync tool is installed next to the host
	Release   string    `json:"release"`   // the build the panel runs, "" for an upstream binary
	Available string    `json:"available"` // a newer build that is ready, "" when none
	CheckedAt time.Time `json:"checkedAt"`
	Busy      bool      `json:"busy"`
	Error     string    `json:"error"`
}

// Config says where the sync tool and its state are. Run replaces the process runner in tests.
type Config struct {
	Exe      string // lucx-plugins-sync
	StateDir string // its state directory (state.json)
	Run      func(ctx context.Context, name string, args ...string) ([]byte, error)
}

// Syncer starts the sync tool and reports what it left.
type Syncer struct {
	cfg Config

	mu      sync.Mutex
	busy    bool
	lastErr string
}

// ErrBusy is returned when a check or an update is already running.
var ErrBusy = errors.New("the panel build is being checked or updated already")

// ErrNotManaged is returned when the sync tool is not installed.
var ErrNotManaged = errors.New("lucx-plugins-sync is not installed next to the plugin system")

func New(cfg Config) *Syncer {
	if cfg.Run == nil {
		cfg.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).CombinedOutput()
		}
	}
	return &Syncer{cfg: cfg}
}

type savedState struct {
	Release        string    `json:"release"`
	CheckedAt      time.Time `json:"checkedAt"`
	Pending        bool      `json:"pending"`
	PendingRelease string    `json:"pendingRelease"`
	LastError      string    `json:"lastError"`
}

func (s *Syncer) managed() bool {
	st, err := os.Stat(s.cfg.Exe)
	return s.cfg.Exe != "" && err == nil && !st.IsDir()
}

// Status reads the sync tool's saved state; it never runs anything.
func (s *Syncer) Status() Status {
	out := Status{Managed: s.managed()}
	var st savedState
	if raw, err := os.ReadFile(filepath.Join(s.cfg.StateDir, "state.json")); err == nil {
		_ = json.Unmarshal(raw, &st)
	}
	out.Release, out.CheckedAt = st.Release, st.CheckedAt
	if st.Pending && st.PendingRelease != "" && st.PendingRelease != st.Release {
		out.Available = st.PendingRelease
	}
	s.mu.Lock()
	out.Busy, out.Error = s.busy, s.lastErr
	s.mu.Unlock()
	if out.Error == "" {
		out.Error = st.LastError
	}
	return out
}

func (s *Syncer) begin() error {
	if !s.managed() {
		return ErrNotManaged
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return ErrBusy
	}
	s.busy = true
	return nil
}

func (s *Syncer) end(err error, output []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.busy = false
	if err != nil {
		s.lastErr = describe(err, output)
		return errors.New(s.lastErr)
	}
	s.lastErr = ""
	return nil
}

// describe puts what the tool said in front of the bare "exit status 1".
func describe(err error, output []byte) string {
	if msg := strings.TrimSpace(string(output)); msg != "" {
		return strings.TrimPrefix(msg, "lucx-plugins: ")
	}
	return err.Error()
}

// Check refreshes the signed builds list and gets a newer build ready, without installing it.
func (s *Syncer) Check(ctx context.Context) error {
	if err := s.begin(); err != nil {
		return err
	}
	out, err := s.cfg.Run(ctx, s.cfg.Exe, "check")
	return s.end(err, out)
}

// Apply installs the newest build and restarts the panel, as "lucx-plugins update" does. It returns when the
// tool is done; the panel's own connection to this call is gone by then, the state is what the page reads.
func (s *Syncer) Apply(ctx context.Context) error {
	if err := s.begin(); err != nil {
		return err
	}
	unit := fmt.Sprintf("--unit=lucx-plugins-update-%d", time.Now().Unix())
	out, err := s.cfg.Run(ctx, "systemd-run", "--pipe", "--wait", "--collect", "--quiet", unit, s.cfg.Exe, "update")
	return s.end(err, out)
}
