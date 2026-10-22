// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"

	"github.com/mhsanaei/3x-ui/v3/internal/config"
)

// DefaultConfig derives locations from the panel's own folders; each can be overridden.
// XUI_PLUGINS_DIR, XUI_PLUGINS_DATA_DIR and XUI_PLUGINS_ARCHIVE_BASE take precedence.
func DefaultConfig() Config {
	return Config{
		CodeDir:      envOr("XUI_PLUGINS_DIR", defaultCodeDir()),
		DataDir:      envOr("XUI_PLUGINS_DATA_DIR", defaultDataDir()),
		RegistryPath: filepath.Join(config.GetDBFolderPath(), "plugins.json"),
		ArchiveBase:  os.Getenv("XUI_PLUGINS_ARCHIVE_BASE"),
		PanelVersion: config.GetBaseVersion(),
		PanelURL:     currentPanelURL,

		CatalogKeys: DefaultCatalogKeys(),
		CatalogBase: os.Getenv("XUI_PLUGINS_CATALOG_BASE"),
	}
}

var panelURLFunc atomic.Pointer[func() string]

// SetPanelURL registers how to learn the address plugins call the panel on;
// the web layer owns the settings, this package stays free of them.
func SetPanelURL(f func() string) { panelURLFunc.Store(&f) }

func currentPanelURL() string {
	if f := panelURLFunc.Load(); f != nil {
		return (*f)()
	}
	return ""
}

func defaultCodeDir() string {
	if inDocker() {
		return filepath.Join(config.GetDBFolderPath(), "plugins")
	}
	bin, err := filepath.Abs(config.GetBinFolderPath())
	if err != nil {
		return filepath.Join(config.GetDBFolderPath(), "plugins")
	}
	return filepath.Join(filepath.Dir(bin), "plugins")
}

func defaultDataDir() string {
	if inDocker() {
		return filepath.Join(config.GetDBFolderPath(), "plugins-data")
	}
	if runtime.GOOS == "linux" {
		return "/var/lib/x-ui-plugins"
	}
	return filepath.Join(config.GetDBFolderPath(), "plugins-data")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
