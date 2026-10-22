// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import (
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
)

// DefaultConfig derives locations from the host's state directory; each can be overridden.
// XUI_PLUGINS_DIR, XUI_PLUGINS_DATA_DIR, XUI_PLUGINS_ARCHIVE_BASE, XUI_PLUGINS_CATALOG_BASE take precedence.
func DefaultConfig(stateDir string) Config {
	return Config{
		CodeDir:      envOr("XUI_PLUGINS_DIR", filepath.Join(stateDir, "plugins")),
		DataDir:      envOr("XUI_PLUGINS_DATA_DIR", defaultDataDir(stateDir)),
		RegistryPath: filepath.Join(stateDir, "plugins.json"),
		ArchiveBase:  os.Getenv("XUI_PLUGINS_ARCHIVE_BASE"),
		PanelVersion: os.Getenv("LUCX_PANEL_VERSION"),
		PanelURL:     currentPanelURL,

		CatalogKeys: DefaultCatalogKeys(),
		CatalogBase: os.Getenv("XUI_PLUGINS_CATALOG_BASE"),
	}
}

var panelURLFunc atomic.Pointer[func() string]

// SetPanelURL registers the address plugins call for the panel's API: the host's gateway.
func SetPanelURL(f func() string) { panelURLFunc.Store(&f) }

func currentPanelURL() string {
	if f := panelURLFunc.Load(); f != nil {
		return (*f)()
	}
	return ""
}

func defaultDataDir(stateDir string) string {
	if inDocker() {
		return filepath.Join(stateDir, "plugins-data")
	}
	if runtime.GOOS == "linux" {
		return "/var/lib/x-ui-plugins"
	}
	return filepath.Join(stateDir, "plugins-data")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
