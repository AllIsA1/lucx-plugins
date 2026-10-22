// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package middleware

import "strings"

// isPluginPageRoute matches the route pattern, not the URL, so a base path in front of it
// still counts and nothing a client types can claim it.
func isPluginPageRoute(fullPath string) bool {
	return strings.Contains(fullPath, "/panel/api/plugins/x/:id/") || strings.Contains(fullPath, "/panel/plugin-frame/:token/")
}
