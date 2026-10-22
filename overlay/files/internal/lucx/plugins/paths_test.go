// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"path/filepath"
	"testing"
)

func TestDefaultConfigEnvOverrides(t *testing.T) {
	t.Setenv("XUI_PLUGINS_DIR", "/srv/p")
	t.Setenv("XUI_PLUGINS_DATA_DIR", "/srv/d")
	t.Setenv("XUI_PLUGINS_ARCHIVE_BASE", "https://mirror.example/gh")
	t.Setenv("XUI_DB_FOLDER", "/srv/db")
	cfg := DefaultConfig()
	if cfg.CodeDir != "/srv/p" || cfg.DataDir != "/srv/d" || cfg.ArchiveBase != "https://mirror.example/gh" {
		t.Errorf("env overrides ignored: %+v", cfg)
	}
	if want := filepath.Join("/srv/db", "plugins.json"); cfg.RegistryPath != want {
		t.Errorf("RegistryPath = %q, want %q", cfg.RegistryPath, want)
	}
}

func TestDefaultCodeDirSitsBesideBinFolder(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XUI_PLUGINS_DIR", "")
	t.Setenv("XUI_BIN_FOLDER", filepath.Join(root, "bin"))
	if got, want := DefaultConfig().CodeDir, filepath.Join(root, "plugins"); got != want {
		t.Errorf("CodeDir = %q, want %q", got, want)
	}
}

func TestDockerKeepsPluginsAndTheirDataOnTheVolume(t *testing.T) {
	t.Setenv("XUI_PLUGINS_DIR", "")
	t.Setenv("XUI_PLUGINS_DATA_DIR", "")
	t.Setenv("XUI_DB_FOLDER", "/srv/db")
	t.Setenv("XUI_IN_DOCKER", "true")
	cfg := DefaultConfig()
	if cfg.CodeDir != "/srv/db/plugins" || cfg.DataDir != "/srv/db/plugins-data" {
		t.Errorf("in Docker CodeDir = %q, DataDir = %q; both must live under the database volume", cfg.CodeDir, cfg.DataDir)
	}
	t.Setenv("XUI_PLUGINS_DIR", "/srv/p")
	t.Setenv("XUI_PLUGINS_DATA_DIR", "/srv/d")
	if cfg := DefaultConfig(); cfg.CodeDir != "/srv/p" || cfg.DataDir != "/srv/d" {
		t.Errorf("explicit settings must win in Docker too: %+v", cfg)
	}
}
