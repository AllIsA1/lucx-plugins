// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Plugin authors copy examples/hello-plugin into their own repositories; a
// noncommercial header on the copied code would bind every plugin built from it.
func exampleDirs(t *testing.T) []string {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join("..", "..", "..", "examples", "*", ManifestFile))
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no examples found: %v", err)
	}
	for i, d := range dirs {
		dirs[i] = filepath.Dir(d)
	}
	return dirs
}

func TestExamplesAreLicensedForCopying(t *testing.T) {
	for _, example := range exampleDirs(t) {
		sources, err := filepath.Glob(filepath.Join(example, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, pattern := range []string{"*.js", "*.css", "*.html"} {
			pages, err := filepath.Glob(filepath.Join(example, "ui", pattern))
			if err != nil {
				t.Fatal(err)
			}
			sources = append(sources, pages...)
		}
		if len(sources) == 0 {
			t.Fatalf("%s: no sources", example)
		}
		for _, src := range sources {
			raw, err := os.ReadFile(src)
			if err != nil {
				t.Fatal(err)
			}
			head := string(raw)
			if i := strings.Index(head, "\npackage "); i >= 0 {
				head = head[:i]
			} else if len(head) > 400 {
				head = head[:400]
			}
			if !strings.Contains(head, "SPDX-License-Identifier: MIT") || strings.Contains(head, "PolyForm") {
				t.Errorf("%s must carry the MIT header only, got:\n%s", src, head)
			}
		}
		license, err := os.ReadFile(filepath.Join(example, "LICENSE"))
		if err != nil || !strings.HasPrefix(string(license), "MIT License") {
			t.Errorf("%s/LICENSE must hold the MIT text (it travels with a copied template): %v", example, err)
		}
		raw, err := os.ReadFile(filepath.Join(example, ManifestFile))
		if err != nil {
			t.Fatal(err)
		}
		m, err := ParseManifest(raw)
		if err != nil {
			t.Fatalf("%s: %v", example, err)
		}
		if m.License != "MIT" {
			t.Errorf("%s: manifest license = %q, want MIT", example, m.License)
		}
	}
}

// Authors copy lucxplugin.go into their plugin; the examples must not drift apart.
func TestExampleSDKCopiesAreIdentical(t *testing.T) {
	var first []byte
	for _, example := range exampleDirs(t) {
		raw, err := os.ReadFile(filepath.Join(example, "lucxplugin.go"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = raw
			continue
		}
		if string(raw) != string(first) {
			t.Errorf("%s/lucxplugin.go differs from the other examples; copy one over the other", example)
		}
	}
	if first == nil {
		t.Fatal("no example carries lucxplugin.go")
	}
}

// Pages carry a copy of the page SDK; they must match examples/sdk/lucx-ui.js, which the tests exercise.
func TestExamplePagesCarryTheCurrentPageSDK(t *testing.T) {
	canonical, err := os.ReadFile(filepath.Join("..", "..", "..", "examples", "sdk", "lucx-ui.js"))
	if err != nil {
		t.Fatal(err)
	}
	copies := 0
	for _, example := range exampleDirs(t) {
		raw, err := os.ReadFile(filepath.Join(example, "ui", "lucx-ui.js"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		copies++
		if string(raw) != string(canonical) {
			t.Errorf("%s/ui/lucx-ui.js differs from examples/sdk/lucx-ui.js; copy it over", example)
		}
	}
	if copies < 2 {
		t.Errorf("found %d page SDK copies, want the ui-hello and hello-plugin pages", copies)
	}
}

// A menu entry that points at a missing file would show an empty frame.
func TestExampleMenuEntriesPointAtRealPages(t *testing.T) {
	for _, example := range exampleDirs(t) {
		raw, err := os.ReadFile(filepath.Join(example, ManifestFile))
		if err != nil {
			t.Fatal(err)
		}
		m, err := ParseManifest(raw)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range m.UI.Menu {
			if _, err := os.Stat(filepath.Join(example, filepath.FromSlash(strings.TrimPrefix(item.Path, "/")))); err != nil {
				t.Errorf("%s: menu entry %q: %v", example, item.Path, err)
			}
		}
	}
}
