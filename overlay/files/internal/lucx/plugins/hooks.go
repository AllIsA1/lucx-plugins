// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"
)

const (
	defaultHookTimeout = 10 * time.Second
	maxHookTries       = 3
)

// callHook POSTs to a hook endpoint on the plugin's socket; any 2xx is success.
func (s *Supervisor) callHook(ctx context.Context, id, path, kind string) error {
	socket, _ := s.runner.Paths(id)
	ctx, cancel := context.WithTimeout(ctx, s.opt.HookTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://plugin"+path, http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("X-LucX-Hook", kind)
	resp, err := newSocketClient(socket, s.opt.HookTimeout).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("plugin answered HTTP %d", resp.StatusCode)
	}
	return nil
}

// installHook runs hooks.install once, after the plugin is first healthy. It
// retries a few times, then gives up until the panel restarts; a failure
// never disables the plugin.
func (s *Supervisor) installHook(ctx context.Context, e Entry, spec ProcessSpec) {
	if spec.Hooks.Install == "" || e.InstallHookDone {
		return
	}
	s.mu.Lock()
	tries := s.hookTries[e.ID]
	if tries >= maxHookTries {
		s.mu.Unlock()
		return
	}
	s.hookTries[e.ID] = tries + 1
	s.mu.Unlock()
	if err := s.callHook(ctx, e.ID, spec.Hooks.Install, "install"); err != nil {
		logger.Warningf("plugins: install hook of %s failed (%d/%d): %v", e.ID, tries+1, maxHookTries, err)
		return
	}
	if err := s.mgr.markInstallHookDone(e.ID); err != nil {
		logger.Warningf("plugins: cannot record the install hook of %s: %v", e.ID, err)
	}
}

// RunUninstallHook is called by Manager.Uninstall while the plugin is still
// running. It never fails the uninstall: a plugin that is not running, or
// whose hook errors or hangs, is simply removed.
func (s *Supervisor) RunUninstallHook(ctx context.Context, e Entry) {
	if s.runner.Available() != nil {
		return
	}
	m, err := s.loadManifest(e)
	if err != nil || m.Hooks.Uninstall == "" {
		return
	}
	if st, err := s.runner.Status(ctx, e.ID); err != nil || st.State != StateRunning {
		return
	}
	if err := s.callHook(ctx, e.ID, m.Hooks.Uninstall, "uninstall"); err != nil {
		logger.Warningf("plugins: uninstall hook of %s failed, removing it anyway: %v", e.ID, err)
	}
}
