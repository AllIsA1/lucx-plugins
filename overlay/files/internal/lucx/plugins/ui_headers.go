// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import "net/http"

// pageCSP keeps a plugin page in an opaque-origin sandbox with no network of its own:
// it can only talk to the panel through the postMessage bridge.
const pageCSP = "sandbox allow-scripts; default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; font-src 'self' data:; connect-src 'none'; form-action 'none'; frame-src 'none'; " +
	"base-uri 'none'; frame-ancestors 'self'"

// SetPageHeaders makes h safe for a response a plugin produced, whatever the plugin sent.
func SetPageHeaders(h http.Header) {
	for _, name := range []string{
		"Set-Cookie", "Access-Control-Allow-Origin", "Access-Control-Allow-Credentials",
		"Content-Security-Policy", "X-Frame-Options",
	} {
		h.Del(name)
	}
	h.Set("Content-Security-Policy", pageCSP)
	h.Set("X-Frame-Options", "SAMEORIGIN")
	h.Set("X-Content-Type-Options", "nosniff")
}
