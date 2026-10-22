// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package service

import "testing"

func TestPanelURLFrom(t *testing.T) {
	cases := []struct {
		name                  string
		listen                string
		port                  int
		cert, key, base, want string
	}{
		{"defaults", "", 2053, "", "", "/", "http://127.0.0.1:2053"},
		{"wildcard listen means loopback", "0.0.0.0", 2053, "", "", "/", "http://127.0.0.1:2053"},
		{"ipv6 wildcard", "::", 2053, "", "", "/", "http://127.0.0.1:2053"},
		{"specific address is kept", "10.0.0.5", 8443, "", "", "/", "http://10.0.0.5:8443"},
		{"tls needs cert and key", "", 2053, "/c.pem", "/k.pem", "/", "https://127.0.0.1:2053"},
		{"cert alone is not tls", "", 2053, "/c.pem", "", "/", "http://127.0.0.1:2053"},
		{"base path without trailing slash", "", 2053, "", "", "/secret/", "http://127.0.0.1:2053/secret"},
		{"base path without leading slash", "", 2053, "", "", "x", "http://127.0.0.1:2053/x"},
		{"ipv6 address is bracketed", "2001:db8::1", 2053, "", "", "/", "http://[2001:db8::1]:2053"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PanelURLFrom(tc.listen, tc.port, tc.cert, tc.key, tc.base); got != tc.want {
				t.Errorf("PanelURLFrom = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEffectivePortHonoursTheEnvironmentOverride(t *testing.T) {
	t.Setenv("XUI_PORT", "")
	if got := effectivePort(2053); got != 2053 {
		t.Errorf("no override: %d", got)
	}
	t.Setenv("XUI_PORT", "19055")
	if got := effectivePort(2053); got != 19055 {
		t.Errorf("the panel listens on XUI_PORT, plugins must call that port; got %d", got)
	}
	t.Setenv("XUI_PORT", "not-a-port")
	if got := effectivePort(2053); got != 2053 {
		t.Errorf("an invalid override is ignored by the panel, so it must be here: %d", got)
	}
}
