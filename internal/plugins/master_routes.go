// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import "net/http"

// masterRoutes is everything a master panel may call on a node's plugin API,
// once the node owner allowed it: list, install a pinned plugin, and control
// plugins the master itself installed. Nothing that edits settings, previews,
// installs from a token or reaches into a plugin's own API is here.
var masterRoutes = join(
	get("/plugins/list", "/plugins/logs/:id"),
	post("/plugins/remote/install", "/plugins/enable/:id", "/plugins/disable/:id",
		"/plugins/uninstall/:id", "/plugins/restart/:id"),
)

// AllowMasterRoute reports whether a master panel may call the route. pattern is
// the gin route pattern relative to /panel/api. HEAD counts as GET.
func AllowMasterRoute(method, pattern string) bool {
	if method == http.MethodHead {
		method = http.MethodGet
	}
	for _, r := range masterRoutes {
		if r.Method == method && r.Pattern == pattern {
			return true
		}
	}
	return false
}

// MasterRoutes lists the routes AllowMasterRoute accepts; tests check each exists.
func MasterRoutes() []APIRoute { return append([]APIRoute(nil), masterRoutes...) }
