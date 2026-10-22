// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/AllIsA1/lucx-plugins/internal/logx"
)

var (
	ErrDisabled   = errors.New("plugins are disabled by XUI_PLUGINS_DISABLED")
	ErrNotStarted = errors.New("plugin manager is not started")
)

var (
	globalMu     sync.RWMutex
	globalMgr    *Manager
	globalSup    *Supervisor
	globalCancel context.CancelFunc
	globalDone   chan struct{}
	globalErr    = ErrNotStarted
)

const safeModeStopTimeout = 15 * time.Second

// SafeMode reports whether the operator turned the plugin system off.
func SafeMode() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("XUI_PLUGINS_DISABLED"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// defaultRunner picks the process backend: systemd, or child processes in the
// Docker image. XUI_PLUGINS_RUNNER (systemd, systemd-user, process) overrides.
func defaultRunner(cfg Config) Runner {
	choice := strings.ToLower(strings.TrimSpace(os.Getenv("XUI_PLUGINS_RUNNER")))
	if choice == "" && inDocker() {
		choice = "process"
	}
	switch choice {
	case "", "systemd":
		return NewSystemdRunner()
	case "process":
		return newHostProcessRunner(cfg)
	case "systemd-user":
		return userSystemdRunner()
	}
	return NewUnavailableRunner(fmt.Sprintf("unknown XUI_PLUGINS_RUNNER %q (use systemd, systemd-user or process)", choice))
}

// userSystemdRunner runs plugins in the user systemd manager, for development without root.
func userSystemdRunner() Runner {
	home, err := os.UserHomeDir()
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if err != nil || runtimeDir == "" {
		return NewUnavailableRunner("systemd-user needs HOME and XDG_RUNTIME_DIR")
	}
	return newUserSystemdRunner(filepath.Join(runtimeDir, "x-ui-plugins"), filepath.Join(home, ".local", "state", "x-ui-plugins"))
}

func inDocker() bool { return strings.EqualFold(os.Getenv("XUI_IN_DOCKER"), "true") }

// Start prepares the process-wide manager and supervisor. It never fails the
// panel: problems are logged and surface through Get, so the panel always
// comes up (Rule 0b).
func Start(cfg Config) {
	Stop()
	globalMu.Lock()
	defer globalMu.Unlock()
	if cfg.Runner == nil {
		cfg.Runner = defaultRunner(cfg)
	}
	if SafeMode() {
		globalErr = ErrDisabled
		logx.Warning("plugins: disabled by XUI_PLUGINS_DISABLED, stopping plugin processes and loading nothing")
		ctx, cancel := context.WithTimeout(context.Background(), safeModeStopTimeout)
		defer cancel()
		if err := StopAllUnits(ctx, cfg.Runner); err != nil {
			logx.Warning("plugins: cannot stop plugin units:", err)
		}
		return
	}
	defer func() {
		if r := recover(); r != nil {
			globalMgr, globalSup, globalErr = nil, nil, fmt.Errorf("plugin manager panicked: %v", r)
			logx.Error("plugins:", globalErr)
		}
	}()
	if cfg.Runner.Available() == nil {
		_, data := cfg.Runner.Paths("x")
		if root := filepath.Dir(data); root != cfg.DataDir {
			logx.Infof("plugins: data directory follows the process runner: %s", root)
			cfg.DataDir = root
		}
	}
	m, err := NewManager(cfg)
	if err != nil {
		globalErr = err
		logx.Warning("plugins: manager unavailable, panel continues without plugins:", err)
		return
	}
	sup := NewSupervisor(m, cfg.Runner, SupervisorOptions{})
	if err := sup.Available(); err != nil {
		logx.Infof("plugins: processes cannot run here (%v); plugins can still be installed", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		sup.Run(ctx)
	}()
	globalMgr, globalSup, globalErr = m, sup, nil
	globalCancel, globalDone = cancel, done
	logx.Infof("plugins: manager ready, %d installed", len(m.List()))
}

// Stop ends the supervisor loop and drops the manager. Processes owned by
// systemd keep running and the next Start adopts them; child processes of the
// panel are stopped here.
func Stop() {
	globalMu.Lock()
	cancel, done, sup := globalCancel, globalDone, globalSup
	globalMgr, globalSup, globalCancel, globalDone, globalErr = nil, nil, nil, nil, ErrNotStarted
	globalMu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	if sup != nil {
		ctx, stop := context.WithTimeout(context.Background(), safeModeStopTimeout)
		defer stop()
		if err := sup.Shutdown(ctx); err != nil {
			logx.Warning("plugins: cannot stop plugin processes:", err)
		}
	}
}

// GetSupervisor returns the running supervisor, nil when plugins are off.
func GetSupervisor() *Supervisor {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return globalSup
}

// Get returns the running manager or the reason there is none.
func Get() (*Manager, error) {
	globalMu.RLock()
	defer globalMu.RUnlock()
	if globalMgr == nil {
		return nil, globalErr
	}
	return globalMgr, nil
}

// AuthenticateToken resolves a plugin bearer token using the running manager.
func AuthenticateToken(token string) (Entry, bool) {
	m, err := Get()
	if err != nil {
		return Entry{}, false
	}
	return m.VerifyToken(token)
}
