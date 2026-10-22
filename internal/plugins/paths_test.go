// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import (
	"path/filepath"
	"testing"
)

func TestDefaultConfigEnvOverrides(t *testing.T) {
	t.Setenv("XUI_PLUGINS_DIR", "/srv/p")
	t.Setenv("XUI_PLUGINS_DATA_DIR", "/srv/d")
	t.Setenv("XUI_PLUGINS_ARCHIVE_BASE", "https://mirror.example/gh")
	t.Setenv("LUCX_PANEL_VERSION", "3.10.2")
	cfg := DefaultConfig("/srv/state")
	if cfg.CodeDir != "/srv/p" || cfg.DataDir != "/srv/d" || cfg.ArchiveBase != "https://mirror.example/gh" || cfg.PanelVersion != "3.10.2" {
		t.Errorf("env overrides ignored: %+v", cfg)
	}
	if want := filepath.Join("/srv/state", "plugins.json"); cfg.RegistryPath != want {
		t.Errorf("RegistryPath = %q, want %q", cfg.RegistryPath, want)
	}
}

func TestDefaultConfigLivesUnderTheHostStateDirectory(t *testing.T) {
	t.Setenv("XUI_PLUGINS_DIR", "")
	t.Setenv("XUI_PLUGINS_DATA_DIR", "")
	t.Setenv("XUI_IN_DOCKER", "")
	cfg := DefaultConfig("/srv/state")
	if cfg.CodeDir != "/srv/state/plugins" {
		t.Errorf("CodeDir = %q", cfg.CodeDir)
	}
	if cfg.DataDir != "/var/lib/x-ui-plugins" {
		t.Errorf("DataDir = %q, the systemd runner's state root", cfg.DataDir)
	}
}

func TestDockerKeepsPluginDataUnderTheStateDirectory(t *testing.T) {
	t.Setenv("XUI_PLUGINS_DATA_DIR", "")
	t.Setenv("XUI_IN_DOCKER", "true")
	if got := DefaultConfig("/srv/state").DataDir; got != "/srv/state/plugins-data" {
		t.Errorf("in Docker DataDir = %q, it must live on the persistent volume", got)
	}
}
