// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package sub

import (
	"path"
	"strings"

	"github.com/gin-gonic/gin"
)

// The names plugin seams use for subscription routes. The real paths are the operator's settings, so a
// plugin cannot know them; it names the kind of subscription instead.
const (
	SeamRouteLinks       = "/links/:subid"
	SeamRouteLinksHWID   = "/links/:subid/hwid-status"
	SeamRouteJSON        = "/json/:subid"
	SeamRouteClash       = "/clash/:subid"
	SeamRouteMihomo      = "/mihomo/:subid"
	SeamRouteClashLegacy = "/clash-legacy/:subid"
	SeamRouteAwg         = "/awg/:subid"
)

// seamRouteResolver maps the gin route a request matched to the name a seam uses for it, or "" for
// anything else the subscription server answers (assets, pages). The first kind to claim a path wins,
// the same order the controller registers them in.
func seamRouteResolver(links, json, clash, awg string) func(*gin.Context) string {
	byPath := map[string]string{}
	claim := func(base, suffix, name string) {
		p := path.Join("/", base, ":subid", suffix)
		if _, taken := byPath[p]; !taken {
			byPath[p] = name
		}
	}
	claim(links, "", SeamRouteLinks)
	claim(links, "hwid-status", SeamRouteLinksHWID)
	claim(json, "", SeamRouteJSON)
	claim(clash, "", SeamRouteClash)
	claim(subMihomoPath, "", SeamRouteMihomo)
	claim(subClashLegacyPath, "", SeamRouteClashLegacy)
	claim(awg, "", SeamRouteAwg)
	return func(c *gin.Context) string {
		return byPath[strings.TrimSuffix(c.FullPath(), "/")]
	}
}
