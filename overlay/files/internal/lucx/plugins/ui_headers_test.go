// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"net/http"
	"strings"
	"testing"
)

func TestPageHeadersOverrideWhateverThePluginSent(t *testing.T) {
	h := http.Header{}
	h.Set("Set-Cookie", "3x-ui=stolen")
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Credentials", "true")
	h.Add("Content-Security-Policy", "default-src *")
	h.Add("X-Frame-Options", "ALLOWALL")
	h.Set("X-Frame-Options", "ALLOWALL")

	SetPageHeaders(h)

	if h.Get("Set-Cookie") != "" || h.Get("Access-Control-Allow-Origin") != "" || h.Get("Access-Control-Allow-Credentials") != "" {
		t.Errorf("browser-privileged headers survived: %v", h)
	}
	if got := h.Values("Content-Security-Policy"); len(got) != 1 || got[0] != pageCSP {
		t.Errorf("CSP = %q, want exactly the page policy", got)
	}
	if got := h.Values("X-Frame-Options"); len(got) != 1 || got[0] != "SAMEORIGIN" {
		t.Errorf("X-Frame-Options = %q", got)
	}
	for _, want := range []string{"sandbox allow-scripts;", "default-src 'none'", "connect-src 'none'", "form-action 'none'", "frame-src 'none'", "base-uri 'none'", "frame-ancestors 'self'"} {
		if !strings.Contains(pageCSP, want) {
			t.Errorf("page CSP lacks %q: %s", want, pageCSP)
		}
	}
	if strings.Contains(pageCSP, "allow-same-origin") {
		t.Error("the page sandbox must never get the panel's origin")
	}
}
