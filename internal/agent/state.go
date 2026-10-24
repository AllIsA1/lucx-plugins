// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// State is what the agent remembers between runs, in StateDir/state.json.
type State struct {
	MaxSequence int64     `json:"maxSequence"` // highest list sequence ever accepted: older lists are refused
	Original    string    `json:"original"`    // sha256 of the upstream binary we replaced
	Ours        string    `json:"ours"`        // sha256 of the build we installed
	Upstream    string    `json:"upstream"`
	Release     string    `json:"release"` // our release tag, <upstream>-plugins.<n>
	SwappedAt   time.Time `json:"swappedAt"`
	CheckedAt   time.Time `json:"checkedAt"`
	Pending     bool      `json:"pending"` // a swap is waiting for the panel's next start
	// PendingRelease is the build that waits, so a person can be told what the next start brings.
	PendingRelease string `json:"pendingRelease,omitempty"`
	LastError      string `json:"lastError"`

	SidecarsSequence int64                   `json:"sidecarsSequence,omitempty"` // highest sidecar list sequence ever accepted
	Sidecars         map[string]SidecarState `json:"sidecars,omitempty"`         // the helper programs this agent installed
	SidecarError     string                  `json:"sidecarError,omitempty"`
}

func (a *Agent) statePath() string { return filepath.Join(a.cfg.StateDir, "state.json") }

// Status reads the saved state.
func (a *Agent) Status() State {
	var st State
	if raw, err := os.ReadFile(a.statePath()); err == nil {
		_ = json.Unmarshal(raw, &st)
	}
	return st
}

func (a *Agent) saveState(st State) error {
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(a.statePath(), append(raw, '\n'), 0o644)
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
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

var errNoVerifiedList = errors.New("no verified builds list is available")
