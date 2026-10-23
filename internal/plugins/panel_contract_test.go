// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
)

// The plugin pages and the SDK live in this repository, the interface they plug into lives in the
// panel's frontend (overlay/). These tests keep the two sides saying the same thing.

const overlayFiles = "../../overlay/files/"

func readOverlay(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(rel)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestReplaceablePagesAreTheOnesThePanelFrontendOffers(t *testing.T) {
	routes := readOverlay(t, "../../overlay/modified/frontend/src/routes.tsx")
	var ui []string
	for _, m := range regexp.MustCompile(`replaceable\('([^']*)'`).FindAllStringSubmatch(routes, -1) {
		ui = append(ui, m[1])
	}
	got := ReplaceableRoutes()
	slices.Sort(ui)
	slices.Sort(got)
	if len(got) == 0 || !slices.Equal(ui, got) {
		t.Errorf("the host lets plugins replace %v, the panel's routes.tsx offers %v", got, ui)
	}
}

func TestExampleMenuIconsExistInThePanelSidebar(t *testing.T) {
	src := readOverlay(t, overlayFiles+"frontend/src/pages/plugins/pluginIcons.ts")
	known := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\s+(?:'([a-z-]+)'|([a-z]+)):\s+\w+Outlined,`).FindAllStringSubmatch(src, -1) {
		known[m[1]+m[2]] = true
	}
	if len(known) < 10 {
		t.Fatalf("parsed only %d icons from pluginIcons.ts: the parser no longer matches the file", len(known))
	}
	manifests, err := filepath.Glob("../../examples/*/lucx-plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, path := range manifests {
		var m struct {
			UI struct {
				Menu []struct {
					Icon string `json:"icon"`
				} `json:"menu"`
			} `json:"ui"`
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for _, item := range m.UI.Menu {
			seen++
			if !known[item.Icon] {
				t.Errorf("%s uses icon %q, which the panel sidebar does not know (it would show the generic one)", path, item.Icon)
			}
		}
	}
	if seen < 2 {
		t.Errorf("only %d example menu entries were checked", seen)
	}
}

// The panel's bridge tests run against a copy of the SDK; it must be the one plugins really use.
func TestThePanelsBridgeTestsUseTheSDKPluginsShip(t *testing.T) {
	want := readOverlay(t, "../../examples/sdk/lucx-ui.js")
	got := readOverlay(t, overlayFiles+"frontend/src/test/fixtures/lucx-ui-sdk.js")
	if want != got {
		t.Error("overlay/files/frontend/src/test/fixtures/lucx-ui-sdk.js differs from examples/sdk/lucx-ui.js: copy the SDK over")
	}
}
