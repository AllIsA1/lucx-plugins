// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import "slices"

// replaceableRoutes are the built-in pages a plugin may stand in for. Settings, Xray, the
// plugin list and the login are absent on purpose: a plugin must not hide its own uninstall.
var replaceableRoutes = []string{"/", "/inbounds", "/clients", "/groups", "/nodes", "/hosts"}

// ReplaceableRoutes lists the built-in pages a plugin may replace.
func ReplaceableRoutes() []string { return slices.Clone(replaceableRoutes) }

// IsReplaceableRoute reports whether a plugin may replace the page at route.
func IsReplaceableRoute(route string) bool { return slices.Contains(replaceableRoutes, route) }
