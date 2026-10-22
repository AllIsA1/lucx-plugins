// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	ManifestFile     = "lucx-plugin.json"
	MaxManifestBytes = 64 << 10
	SupportedAPI     = "1"

	TypeUI      = "ui"
	TypeProcess = "process"
	TypeHook    = "hook"

	ScopeMaster = "master"
	ScopeNode   = "node"
	ScopeBoth   = "both"

	maxArgs   = 32
	maxMenu   = 10
	maxPathIn = 200
)

var (
	idPattern          = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$`)
	versionPattern     = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)
	iconPattern        = regexp.MustCompile(`^[a-z-]{0,30}$`)
	placeholderPattern = regexp.MustCompile(`\$\{([a-z]+)\}`)
	menuPathPattern    = regexp.MustCompile(`^/ui/[A-Za-z0-9._/-]+$`)
	routePattern       = regexp.MustCompile(`^/([a-z0-9][a-z0-9/_-]*)?$`)
	hookPathPattern    = regexp.MustCompile(`^/_lucx/[a-z0-9_/-]{1,60}$`)
)

// Manifest is the parsed lucx-plugin.json of a plugin repository.
type Manifest struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Version         string   `json:"version"`
	APIVersion      string   `json:"apiVersion"`
	MinPanelVersion string   `json:"minPanelVersion,omitempty"`
	Description     string   `json:"description,omitempty"`
	Author          string   `json:"author,omitempty"`
	License         string   `json:"license,omitempty"`
	Type            string   `json:"type"`
	Scope           string   `json:"scope,omitempty"`
	Run             *RunSpec `json:"run,omitempty"`
	Permissions     []string `json:"permissions"`
	UI              UISpec   `json:"ui"`
	Hooks           HookSpec `json:"hooks"`
}

// RunSpec describes how the panel starts a process plugin.
type RunSpec struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

// UISpec lists the interface entries a plugin contributes.
type UISpec struct {
	Menu     []MenuItem `json:"menu,omitempty"`
	Replaces []string   `json:"replaces,omitempty"`
}

// MenuItem is a sidebar entry that opens a page under the plugin's ui/ directory.
type MenuItem struct {
	Title string `json:"title"`
	Icon  string `json:"icon,omitempty"`
	Path  string `json:"path"`
}

// HookSpec names the plugin endpoints the panel calls on install and uninstall.
type HookSpec struct {
	Install   string `json:"install,omitempty"`
	Uninstall string `json:"uninstall,omitempty"`
}

// ParseManifest decodes and validates a manifest.
func ParseManifest(data []byte) (*Manifest, error) {
	if len(data) > MaxManifestBytes {
		return nil, fmt.Errorf("manifest is larger than %d bytes", MaxManifestBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("manifest is not valid JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("manifest has data after the JSON document")
	}
	if m.Scope == "" {
		m.Scope = ScopeMaster
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Validate checks every field that reaches paths, process arguments or the UI.
func (m *Manifest) Validate() error {
	if !idPattern.MatchString(m.ID) {
		return fmt.Errorf("invalid id %q: use 3-40 lowercase letters, digits and dashes", m.ID)
	}
	if n := len([]rune(m.Name)); n < 1 || n > 60 {
		return errors.New("invalid name: must be 1-60 characters")
	}
	if !versionPattern.MatchString(m.Version) {
		return fmt.Errorf("invalid version %q: must be semver like 1.2.3", m.Version)
	}
	if m.APIVersion != SupportedAPI {
		return fmt.Errorf("unsupported apiVersion %q (this panel supports %q)", m.APIVersion, SupportedAPI)
	}
	if m.MinPanelVersion != "" {
		if _, err := parseVersion(m.MinPanelVersion); err != nil {
			return fmt.Errorf("invalid minPanelVersion: %w", err)
		}
	}
	for field, limit := range map[string]struct {
		val string
		max int
	}{"description": {m.Description, 500}, "author": {m.Author, 100}, "license": {m.License, 50}} {
		if len([]rune(limit.val)) > limit.max {
			return fmt.Errorf("invalid %s: longer than %d characters", field, limit.max)
		}
	}
	switch m.Type {
	case TypeUI, TypeProcess, TypeHook:
	default:
		return fmt.Errorf("invalid type %q: use ui, process or hook", m.Type)
	}
	switch m.Scope {
	case ScopeMaster, ScopeNode, ScopeBoth:
	default:
		return fmt.Errorf("invalid scope %q: use master, node or both", m.Scope)
	}
	if err := m.validatePermissions(); err != nil {
		return err
	}
	if err := m.validateRun(); err != nil {
		return err
	}
	return m.validateUI()
}

func (m *Manifest) validatePermissions() error {
	for _, p := range m.Permissions {
		if err := ValidatePermission(p); err != nil {
			return err
		}
		if m.Type == TypeUI && needsProcess(p) {
			return fmt.Errorf("permission %q is not allowed for ui plugins (they have no process)", p)
		}
	}
	return nil
}

func needsProcess(p string) bool {
	return p == permNetwork || strings.HasPrefix(p, "events:") || strings.HasPrefix(p, "pipeline:")
}

func (m *Manifest) validateRun() error {
	if m.Type == TypeUI {
		if m.Run != nil || m.Hooks != (HookSpec{}) {
			return errors.New("ui plugins cannot have run or hooks")
		}
		return nil
	}
	if m.Run == nil || m.Run.Command == "" {
		return errors.New("run.command is required for process and hook plugins")
	}
	if err := validateRunToken("run.command", m.Run.Command); err != nil {
		return err
	}
	if !filepath.IsLocal(filepath.FromSlash(substitutePlaceholders(m.Run.Command))) ||
		strings.HasPrefix(m.Run.Command, "-") {
		return fmt.Errorf("run.command %q must be a relative path inside the plugin", m.Run.Command)
	}
	if len(m.Run.Args) > maxArgs {
		return fmt.Errorf("run.args has more than %d entries", maxArgs)
	}
	for _, a := range m.Run.Args {
		if err := validateRunToken("run.args", a); err != nil {
			return err
		}
	}
	for field, hook := range map[string]string{"hooks.install": m.Hooks.Install, "hooks.uninstall": m.Hooks.Uninstall} {
		if hook != "" && !hookPathPattern.MatchString(hook) {
			return fmt.Errorf("invalid %s %q: must start with /_lucx/", field, hook)
		}
	}
	return nil
}

func validateRunToken(field, v string) error {
	if len(v) > 256 || strings.ContainsAny(v, "\x00\n\r\\") {
		return fmt.Errorf("invalid %s: %q has forbidden characters or is too long", field, v)
	}
	for _, match := range placeholderPattern.FindAllStringSubmatch(v, -1) {
		switch match[1] {
		case "arch", "socket", "data":
		default:
			return fmt.Errorf("invalid %s: unknown placeholder ${%s}", field, match[1])
		}
	}
	return nil
}

func substitutePlaceholders(v string) string {
	return placeholderPattern.ReplaceAllString(v, "x")
}

func (m *Manifest) validateUI() error {
	if len(m.UI.Menu) == 0 && len(m.UI.Replaces) == 0 {
		return nil
	}
	if m.Type == TypeHook {
		return errors.New("hook plugins cannot have ui.menu or ui.replaces")
	}
	perms := normalizePermissions(m.Permissions)
	has := func(p string) bool {
		for _, q := range perms {
			if q == p {
				return true
			}
		}
		return false
	}
	if len(m.UI.Menu) > maxMenu {
		return fmt.Errorf("ui.menu has more than %d entries", maxMenu)
	}
	if len(m.UI.Menu) > 0 && !has(permUIMenu) {
		return fmt.Errorf("ui.menu requires the %q permission", permUIMenu)
	}
	for _, item := range m.UI.Menu {
		if n := len([]rune(item.Title)); n < 1 || n > 40 {
			return errors.New("invalid ui.menu title: must be 1-40 characters")
		}
		if !iconPattern.MatchString(item.Icon) {
			return fmt.Errorf("invalid ui.menu icon %q", item.Icon)
		}
		if len(item.Path) > maxPathIn || !menuPathPattern.MatchString(item.Path) || path.Clean(item.Path) != item.Path {
			return fmt.Errorf("invalid ui.menu path %q: must be a clean path under /ui/", item.Path)
		}
	}
	for _, route := range m.UI.Replaces {
		if !routePattern.MatchString(route) || path.Clean(route) != route {
			return fmt.Errorf("invalid ui.replaces route %q", route)
		}
		if !IsReplaceableRoute(route) {
			return fmt.Errorf("ui.replaces %q: plugins cannot replace this page", route)
		}
		if !has("ui:replace:" + route) {
			return fmt.Errorf("ui.replaces %q requires the permission \"ui:replace:%s\"", route, route)
		}
	}
	return nil
}

// CompatibleWith checks minPanelVersion against the running panel version.
func (m *Manifest) CompatibleWith(panelVersion string) error {
	if m.MinPanelVersion == "" {
		return nil
	}
	need, err := parseVersion(m.MinPanelVersion)
	if err != nil {
		return err
	}
	have, err := parseVersion(panelVersion)
	if err != nil {
		return fmt.Errorf("cannot compare with panel version %q: %w", panelVersion, err)
	}
	for i := range need {
		if have[i] != need[i] {
			if have[i] > need[i] {
				return nil
			}
			return fmt.Errorf("plugin needs panel %s or newer (running %s)", m.MinPanelVersion, panelVersion)
		}
	}
	return nil
}

func parseVersion(v string) ([3]int, error) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, fmt.Errorf("version %q is not major.minor.patch", v)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, fmt.Errorf("version %q is not numeric", v)
		}
		out[i] = n
	}
	return out, nil
}
