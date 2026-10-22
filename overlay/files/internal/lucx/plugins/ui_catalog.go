// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import "slices"

// replaceableRoutes are the built-in pages a plugin may stand in for. Settings, Xray, the
// plugin list and the login are absent on purpose: a plugin must not hide its own uninstall.
var replaceableRoutes = []string{"/", "/inbounds", "/clients", "/groups", "/nodes", "/hosts"}

// ReplaceableRoutes lists the built-in pages a plugin may replace.
func ReplaceableRoutes() []string { return slices.Clone(replaceableRoutes) }

// IsReplaceableRoute reports whether a plugin may replace the page at route.
func IsReplaceableRoute(route string) bool { return slices.Contains(replaceableRoutes, route) }
