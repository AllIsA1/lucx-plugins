// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package web

import (
	"os"
	"strings"
	"testing"
)

// The link to the plugin host is wired into start and stop by a few lines in web.go. They are easy
// to lose in a merge with a new upstream release, and nothing else would notice: the panel would
// start fine and plugins would silently do nothing. This keeps the wiring in view.
func TestWebServerWiresThePluginHostLink(t *testing.T) {
	src, err := os.ReadFile("web.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range []string{
		"hostlink.Start()",
		"hostlink.ServeInternal(engine,",
		`s.bus.Subscribe("plugins", hostlink.HandleEvent)`,
		"hostlink.Stop()",
	} {
		if !strings.Contains(string(src), call) {
			t.Errorf("web.go no longer calls %s: the plugin host link would not work", call)
		}
	}
}
