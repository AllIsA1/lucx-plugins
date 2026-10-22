// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package middleware

import "strings"

// isPluginPageRoute matches the route pattern, not the URL, so a base path in front of it
// still counts and nothing a client types can claim it.
func isPluginPageRoute(fullPath string) bool {
	return strings.Contains(fullPath, "/panel/api/plugins/x/:id/") || strings.Contains(fullPath, "/panel/plugin-frame/:token/")
}
