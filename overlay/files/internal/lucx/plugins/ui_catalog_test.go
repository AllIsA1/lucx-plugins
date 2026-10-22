// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import "testing"

func TestOnlyCatalogRoutesAreReplaceable(t *testing.T) {
	for _, route := range []string{"/", "/inbounds", "/clients", "/groups", "/nodes", "/hosts"} {
		if !IsReplaceableRoute(route) {
			t.Errorf("%q must be replaceable", route)
		}
	}
	// A plugin must never be able to hide settings, the plugin list (its own uninstall) or the login.
	for _, route := range []string{"/settings", "/xray", "/plugins", "/api-docs", "/sponsors", "/login", "/clients/x", "/hosts/", "/Clients", ""} {
		if IsReplaceableRoute(route) {
			t.Errorf("%q must not be replaceable", route)
		}
	}
}

func TestReplacePermissionNeedsAReplaceableRoute(t *testing.T) {
	for perm, ok := range map[string]bool{
		"ui:replace:/clients":  true,
		"ui:replace:/":         true,
		"ui:replace:/settings": false,
		"ui:replace:/plugins":  false,
	} {
		if err := ValidatePermission(perm); (err == nil) != ok {
			t.Errorf("ValidatePermission(%q) = %v, want ok=%v", perm, err, ok)
		}
	}
}

func TestManifestReplacesOnlyReplaceablePages(t *testing.T) {
	build := func(route string) []byte {
		return manifestJSON(t, func(m map[string]any) {
			m["type"] = "ui"
			delete(m, "run")
			m["permissions"] = []string{"ui:replace:" + route}
			m["ui"] = map[string]any{"replaces": []string{route}}
		})
	}
	if _, err := ParseManifest(build("/clients")); err != nil {
		t.Errorf("replacing /clients: %v", err)
	}
	if _, err := ParseManifest(build("/")); err != nil {
		t.Errorf("replacing the dashboard: %v", err)
	}
	if _, err := ParseManifest(build("/settings")); err == nil {
		t.Error("a plugin that replaces the settings page was accepted")
	}
}
