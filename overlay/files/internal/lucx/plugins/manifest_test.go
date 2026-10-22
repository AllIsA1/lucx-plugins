// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"strings"
	"testing"
)

func TestParseManifestValid(t *testing.T) {
	m, err := ParseManifest(manifestJSON(t, nil))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if m.ID != "traffic-alerts" || m.Version != "1.2.0" || m.Type != TypeProcess {
		t.Errorf("unexpected manifest: %+v", m)
	}
	if m.Scope != ScopeMaster {
		t.Errorf("Scope = %q, want default %q", m.Scope, ScopeMaster)
	}
	if m.Run == nil || m.Run.Command != "bin/${arch}/alerts" {
		t.Errorf("Run = %+v", m.Run)
	}
}

func TestParseManifestUIPlugin(t *testing.T) {
	data := manifestJSON(t, func(m map[string]any) {
		m["type"] = "ui"
		delete(m, "run")
		m["permissions"] = []string{"ui:menu", "api:clients:read"}
		m["ui"] = map[string]any{
			"menu": []map[string]any{{"title": "Alerts", "icon": "bell", "path": "/ui/index.html"}},
		}
	})
	m, err := ParseManifest(data)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if len(m.UI.Menu) != 1 || m.UI.Menu[0].Path != "/ui/index.html" {
		t.Errorf("menu = %+v", m.UI.Menu)
	}
}

func TestParseManifestRejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(m map[string]any)
		want   string
	}{
		{"id with traversal", func(m map[string]any) { m["id"] = "../etc" }, "id"},
		{"id too short", func(m map[string]any) { m["id"] = "ab" }, "id"},
		{"id upper case", func(m map[string]any) { m["id"] = "Alerts" }, "id"},
		{"id with space", func(m map[string]any) { m["id"] = "my plugin" }, "id"},
		{"id too long", func(m map[string]any) { m["id"] = strings.Repeat("a", 41) }, "id"},
		{"id leading dash", func(m map[string]any) { m["id"] = "-abc" }, "id"},
		{"empty name", func(m map[string]any) { m["name"] = "" }, "name"},
		{"version not semver", func(m map[string]any) { m["version"] = "1.0" }, "version"},
		{"unsupported apiVersion", func(m map[string]any) { m["apiVersion"] = "2" }, "apiVersion"},
		{"unknown type", func(m map[string]any) { m["type"] = "weird" }, "type"},
		{"unknown scope", func(m map[string]any) { m["scope"] = "everything" }, "scope"},
		{"unknown permission", func(m map[string]any) { m["permissions"] = []string{"root"} }, "permission"},
		{"unknown api resource", func(m map[string]any) { m["permissions"] = []string{"api:users:write"} }, "permission"},
		{"bad event name", func(m map[string]any) { m["permissions"] = []string{"events:Bad"} }, "permission"},
		{"process without run", func(m map[string]any) { delete(m, "run") }, "run"},
		{"ui with run", func(m map[string]any) {
			m["type"] = "ui"
			m["permissions"] = []string{}
		}, "run"},
		{"ui with network", func(m map[string]any) {
			m["type"] = "ui"
			delete(m, "run")
			m["permissions"] = []string{"network"}
		}, "permission"},
		{"run command traversal", func(m map[string]any) {
			m["run"] = map[string]any{"command": "../x"}
		}, "run"},
		{"run command absolute", func(m map[string]any) {
			m["run"] = map[string]any{"command": "/bin/sh"}
		}, "run"},
		{"run unknown placeholder", func(m map[string]any) {
			m["run"] = map[string]any{"command": "bin/${evil}"}
		}, "run"},
		{"run arg with NUL", func(m map[string]any) {
			m["run"] = map[string]any{"command": "bin/x", "args": []string{"a\x00b"}}
		}, "run"},
		{"menu path outside ui", func(m map[string]any) {
			m["permissions"] = []string{"ui:menu"}
			m["ui"] = map[string]any{"menu": []map[string]any{{"title": "x", "path": "/etc/passwd"}}}
		}, "menu"},
		{"menu path traversal", func(m map[string]any) {
			m["permissions"] = []string{"ui:menu"}
			m["ui"] = map[string]any{"menu": []map[string]any{{"title": "x", "path": "/ui/../secret"}}}
		}, "menu"},
		{"menu without permission", func(m map[string]any) {
			m["ui"] = map[string]any{"menu": []map[string]any{{"title": "x", "path": "/ui/a.html"}}}
		}, "menu"},
		{"replaces without permission", func(m map[string]any) {
			m["ui"] = map[string]any{"replaces": []string{"/hosts"}}
		}, "replaces"},
		{"hook with menu", func(m map[string]any) {
			m["type"] = "hook"
			m["permissions"] = []string{"ui:menu"}
			m["ui"] = map[string]any{"menu": []map[string]any{{"title": "x", "path": "/ui/a.html"}}}
		}, "menu"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseManifest(manifestJSON(t, tc.mutate))
			if err == nil {
				t.Fatalf("ParseManifest accepted invalid manifest")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestParseManifestMalformed(t *testing.T) {
	cases := map[string]string{
		"not json":      "{",
		"trailing data": string(manifestJSON(t, nil)) + `{"x":1}`,
		"oversize":      `{"id":"abc","description":"` + strings.Repeat("a", MaxManifestBytes) + `"}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseManifest([]byte(raw)); err == nil {
				t.Fatal("ParseManifest accepted malformed input")
			}
		})
	}
}

func TestCompatibleWith(t *testing.T) {
	cases := []struct {
		min, panel string
		ok         bool
	}{
		{"", "3.10.2", true},
		{"3.10.2", "3.10.2", true},
		{"3.10.2", "3.10.10", true},
		{"3.10.2", "3.9.9", false},
		{"3.10.2", "v4.0.0", true},
		{"3.10.2", "3.10.2-lucx.304", true},
		{"3.10.3", "3.10.2+build5", false},
	}
	for _, tc := range cases {
		m := &Manifest{MinPanelVersion: tc.min}
		err := m.CompatibleWith(tc.panel)
		if (err == nil) != tc.ok {
			t.Errorf("min=%q panel=%q: err=%v, want ok=%v", tc.min, tc.panel, err, tc.ok)
		}
	}
}

func TestCompatibleWithUnparsablePanelVersion(t *testing.T) {
	m := &Manifest{MinPanelVersion: "3.10.2"}
	if err := m.CompatibleWith("dev"); err == nil {
		t.Fatal("unparsable panel version must fail closed when a minimum is required")
	}
	if err := (&Manifest{}).CompatibleWith("dev"); err != nil {
		t.Fatalf("no minimum required, got %v", err)
	}
}

func TestIsDangerousPermission(t *testing.T) {
	cases := map[string]bool{
		"network":                 true,
		"api:clients:write":       true,
		"api:inbounds:write":      true,
		"pipeline:xray.config":    true,
		"ui:replace:/hosts":       true,
		"api:clients:read":        false,
		"events:client.created":   false,
		"ui:menu":                 false,
		"api:server:read":         false,
		"events:xray.crash":       false,
		"pipeline:sub.link":       true,
		"ui:replace:/inbounds/ab": true,
	}
	for perm, want := range cases {
		if got := IsDangerousPermission(perm); got != want {
			t.Errorf("IsDangerousPermission(%q) = %v, want %v", perm, got, want)
		}
	}
}

func TestSamePermissionSet(t *testing.T) {
	if !SamePermissionSet([]string{"a", "b"}, []string{"b", "a", "a"}) {
		t.Error("order and duplicates must not matter")
	}
	if SamePermissionSet([]string{"a"}, []string{"a", "b"}) {
		t.Error("an extra permission must not match")
	}
	if SamePermissionSet([]string{"a", "b"}, []string{"a"}) {
		t.Error("a missing permission must not match")
	}
}
