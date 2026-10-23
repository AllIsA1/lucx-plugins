// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package hostapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/AllIsA1/lucx-plugins/internal/hostupdate"
	"github.com/AllIsA1/lucx-plugins/internal/plugins"
)

// SystemUpdater is the part of the host that replaces the host itself with a newer build.
type SystemUpdater interface {
	Status() hostupdate.Status
	Check(ctx context.Context) error
	Apply(ctx context.Context) error
}

// PluginService exposes the plugin manager to the web layer. The manager may be
// absent (safe mode, corrupt registry); State still answers so the UI can say why.
type PluginService struct {
	Provider   func() (*plugins.Manager, error)
	Supervisor func() *plugins.Supervisor
	// UpdateHealthTimeout is how long an updated process gets to become healthy before the update is undone.
	UpdateHealthTimeout time.Duration
	// Updater replaces the host itself; nil means this host does not update itself.
	Updater SystemUpdater
	// Nodes manages the plugins of the master panel's nodes; nil on a host that has no link to a panel's nodes.
	Nodes *NodePluginsService
	// RestartHost ends the process so it can start again as the new build; it is called after the update was answered.
	RestartHost func()
}

const defaultUpdateHealthTimeout = 30 * time.Second

// NewPluginService binds the service to the process-wide plugin manager.
func NewPluginService() *PluginService {
	return &PluginService{Provider: plugins.Get, Supervisor: plugins.GetSupervisor}
}

// PluginView is an installed plugin plus the live state of its process.
type PluginView struct {
	plugins.Entry
	Runtime *plugins.PluginRuntime `json:"runtime"`
}

// PluginsState is what the Plugins page renders.
type PluginsState struct {
	Available             bool         `json:"available"`
	AllowMasterManagement bool         `json:"allowMasterManagement"`
	ProcessesAvailable    bool         `json:"processesAvailable"`
	ProcessesError        string       `json:"processesError"`
	SafeMode              bool         `json:"safeMode"`
	Error                 string       `json:"error"`
	ArchiveBase           string       `json:"archiveBase"`
	CustomArchiveBase     string       `json:"customArchiveBase"`
	DefaultArchiveBase    string       `json:"defaultArchiveBase"`
	Plugins               []PluginView `json:"plugins"`

	ProcessesIsolation *plugins.Isolation `json:"processesIsolation,omitempty"`
}

func (s *PluginService) supervisor() *plugins.Supervisor {
	if s.Supervisor == nil {
		return nil
	}
	return s.Supervisor()
}

func (s *PluginService) kick() {
	if sup := s.supervisor(); sup != nil {
		sup.Kick()
	}
}

// State reports installed plugins and why the manager might be unavailable.
func (s *PluginService) State() PluginsState {
	st := PluginsState{
		SafeMode:           plugins.SafeMode(),
		ArchiveBase:        plugins.DefaultArchiveBase,
		DefaultArchiveBase: plugins.DefaultArchiveBase,
		Plugins:            []PluginView{},
	}
	m, err := s.Provider()
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.Available = true
	st.AllowMasterManagement = m.AllowMasterManagement()
	st.ArchiveBase = m.ArchiveBase()
	st.CustomArchiveBase = m.CustomArchiveBase()
	sup := s.supervisor()
	if sup == nil {
		st.ProcessesError = "the process supervisor is not running"
	} else if err := sup.Available(); err != nil {
		st.ProcessesError = err.Error()
	} else {
		st.ProcessesAvailable = true
		iso := sup.Isolation()
		st.ProcessesIsolation = &iso
	}
	for _, e := range m.List() {
		view := PluginView{Entry: e}
		if sup != nil {
			if rt, ok := sup.Runtime(e.ID); ok {
				view.Runtime = &rt
			}
		}
		st.Plugins = append(st.Plugins, view)
	}
	return st
}

// Preview downloads and validates a plugin repository without installing it.
func (s *PluginService) Preview(ctx context.Context, url string) (*plugins.Preview, error) {
	m, err := s.Provider()
	if err != nil {
		return nil, err
	}
	return m.Preview(ctx, url)
}

// Install finishes a previewed install once the operator confirmed the permissions.
func (s *PluginService) Install(token string, permissions []string) (plugins.Entry, error) {
	m, err := s.Provider()
	if err != nil {
		return plugins.Entry{}, err
	}
	return m.Install(token, permissions)
}

// SetEnabled turns an installed plugin on or off.
func (s *PluginService) SetEnabled(id string, enabled bool) error {
	m, err := s.Provider()
	if err != nil {
		return err
	}
	if err := m.SetEnabled(id, enabled); err != nil {
		return err
	}
	s.kick()
	return nil
}

// Restart restarts an enabled plugin's process.
func (s *PluginService) Restart(ctx context.Context, id string) error {
	sup := s.supervisor()
	if sup == nil {
		return errors.New("the process supervisor is not running")
	}
	return sup.Restart(ctx, id)
}

// Logs returns the most recent lines a plugin process wrote.
func (s *PluginService) Logs(ctx context.Context, id string, lines int) ([]string, error) {
	sup := s.supervisor()
	if sup == nil {
		return nil, errors.New("the process supervisor is not running")
	}
	out, err := sup.Logs(ctx, id, lines)
	if out == nil {
		out = []string{}
	}
	return out, err
}

// Uninstall removes a plugin and optionally its data.
func (s *PluginService) Uninstall(id string, removeData bool) error {
	m, err := s.Provider()
	if err != nil {
		return err
	}
	return m.Uninstall(id, removeData)
}

// SetArchiveBase stores the operator's archive mirror; "" restores the default.
func (s *PluginService) SetArchiveBase(base string) error {
	m, err := s.Provider()
	if err != nil {
		return err
	}
	return m.SetArchiveBase(base)
}

// Proxy answers a request for a plugin's own URL space: static pages of interface plugins
// come from disk, everything else is forwarded to the plugin's process on behalf of user.
func (s *PluginService) Proxy(w http.ResponseWriter, r *http.Request, id, subpath, user string) error {
	known := false
	if m, err := s.Provider(); err == nil {
		if e, ok := m.Get(id); ok {
			known = true
			if e.Type == plugins.TypeUI {
				return m.ServeUI(w, r, id, subpath)
			}
		}
	}
	sup := s.supervisor()
	if sup == nil {
		if known {
			return plugins.ErrNotRunning
		}
		return plugins.ErrNotFound
	}
	return sup.Proxy(w, r, id, subpath, user)
}

// PreviewUpdate downloads a plugin's update and says what it would change.
func (s *PluginService) PreviewUpdate(ctx context.Context, id, ref string) (*plugins.UpdatePreview, error) {
	m, err := s.Provider()
	if err != nil {
		return nil, err
	}
	return m.PreviewUpdate(ctx, id, ref)
}

// ApplyUpdate installs a previewed update and, for a running plugin, undoes it when the new process
// does not become healthy in time.
func (s *PluginService) ApplyUpdate(ctx context.Context, id, token string, permissions []string) (plugins.Entry, error) {
	m, err := s.Provider()
	if err != nil {
		return plugins.Entry{}, err
	}
	entry, err := m.ApplyUpdate(ctx, id, token, permissions)
	if err != nil {
		return plugins.Entry{}, err
	}
	s.kick()
	healthErr := s.waitHealthy(ctx, entry)
	if healthErr == nil {
		return entry, nil
	}
	restored, rollbackErr := m.Rollback(ctx, id)
	s.kick()
	if rollbackErr != nil {
		return entry, fmt.Errorf("%w and going back failed too (%w): %w", plugins.ErrUpdateRolledBack, rollbackErr, healthErr)
	}
	return restored, fmt.Errorf("%w: %w", plugins.ErrUpdateRolledBack, healthErr)
}

// Rollback puts the version from before the last update back.
func (s *PluginService) Rollback(ctx context.Context, id string) (plugins.Entry, error) {
	m, err := s.Provider()
	if err != nil {
		return plugins.Entry{}, err
	}
	entry, err := m.Rollback(ctx, id)
	if err == nil {
		s.kick()
	}
	return entry, err
}

// waitHealthy returns nil at once for a plugin that has no process to watch.
func (s *PluginService) waitHealthy(ctx context.Context, e plugins.Entry) error {
	sup := s.supervisor()
	if !e.Enabled || (e.Type != plugins.TypeProcess && e.Type != plugins.TypeHook) || sup == nil || sup.Available() != nil {
		return nil
	}
	timeout := s.UpdateHealthTimeout
	if timeout <= 0 {
		timeout = defaultUpdateHealthTimeout
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		rt, _ := sup.Runtime(e.ID)
		switch {
		case rt.State == plugins.StateRunning && rt.Healthy:
			return nil
		case rt.State == plugins.StateFailed:
			return fmt.Errorf("the new version failed to start: %s", firstNonEmpty(rt.Detail, rt.LastError))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("the new version was not healthy after %s (%s): %s", timeout, rt.State, firstNonEmpty(rt.HealthError, rt.Detail, rt.LastError))
		case <-tick.C:
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return "no detail"
}

// Catalog returns the signed catalog; when plugins are off it says so instead of failing.
func (s *PluginService) Catalog(ctx context.Context, force bool) plugins.CatalogView {
	m, err := s.Provider()
	if err != nil {
		return plugins.CatalogView{State: plugins.CatalogError, Error: err.Error(), Plugins: []plugins.CatalogPlugin{}}
	}
	return m.Catalog(ctx, force)
}

// PreviewCatalog downloads a catalog plugin at its signed commit, checked against the index.
func (s *PluginService) PreviewCatalog(ctx context.Context, id string) (*plugins.Preview, error) {
	m, err := s.Provider()
	if err != nil {
		return nil, err
	}
	return m.PreviewCatalog(ctx, id)
}

// PreviewCatalogUpdate previews an update to the commit the catalog signed.
func (s *PluginService) PreviewCatalogUpdate(ctx context.Context, id string) (*plugins.UpdatePreview, error) {
	m, err := s.Provider()
	if err != nil {
		return nil, err
	}
	return m.PreviewCatalogUpdate(ctx, id)
}

// SetCatalogBase stores the operator's catalog mirror; "" restores the default.
func (s *PluginService) SetCatalogBase(base string) error {
	m, err := s.Provider()
	if err != nil {
		return err
	}
	return m.SetCatalogBase(base)
}

// FrameToken mints the URL token a sandboxed plugin page needs, since it has no cookie.
func (s *PluginService) FrameToken(id, user string) (string, time.Time, error) {
	m, err := s.Provider()
	if err != nil {
		return "", time.Time{}, err
	}
	return m.IssueFrameToken(id, user)
}

// VerifyFrameToken resolves a page token to its plugin and user.
func (s *PluginService) VerifyFrameToken(token string) (plugins.Entry, string, bool) {
	m, err := s.Provider()
	if err != nil {
		return plugins.Entry{}, "", false
	}
	return m.VerifyFrameToken(token)
}

// UI is what plugins add to the interface now; empty when plugins are off.
func (s *PluginService) UI() plugins.UIState {
	m, err := s.Provider()
	if err != nil {
		return plugins.BuildUIState(nil)
	}
	return plugins.BuildUIState(m.List())
}

// AllowMasterManagement reports whether a master panel may manage plugins here;
// false when the plugin system is unavailable.
func (s *PluginService) AllowMasterManagement() bool {
	m, err := s.Provider()
	return err == nil && m.AllowMasterManagement()
}

// SetMasterManagement records the node owner's decision to let a master in or out.
func (s *PluginService) SetMasterManagement(allow bool) error {
	m, err := s.Provider()
	if err != nil {
		return err
	}
	return m.SetAllowMasterManagement(allow)
}

// IsManagedByMaster reports whether a master panel installed the plugin.
func (s *PluginService) IsManagedByMaster(id string) bool {
	m, err := s.Provider()
	if err != nil {
		return false
	}
	for _, e := range m.List() {
		if e.ID == id {
			return e.ManagedByMaster
		}
	}
	return false
}

// InstallPinned installs exactly the commit, manifest and permissions a master approved.
func (s *PluginService) InstallPinned(ctx context.Context, req plugins.PinnedInstall) (plugins.Entry, error) {
	m, err := s.Provider()
	if err != nil {
		return plugins.Entry{}, err
	}
	return m.InstallPinned(ctx, req)
}
