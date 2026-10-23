// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostapi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/AllIsA1/lucx-plugins/internal/plugins"
)

// PluginFrameController serves plugin pages to the sandboxed iframe. That frame has no origin
// and so no cookie: its URL carries a short-lived token instead, and only GET under /ui/ is open.
type PluginFrameController struct {
	svc *PluginService
}

// NewPluginFrameController registers /panel/plugin-frame/<token>/ui/... on g; it sits outside the API group's login.
func NewPluginFrameController(g *gin.RouterGroup, svc *PluginService) *PluginFrameController {
	a := &PluginFrameController{svc: svc}
	g.GET("/panel/plugin-frame/:token/*path", a.frame)
	g.HEAD("/panel/plugin-frame/:token/*path", a.frame)
	return a
}

func (a *PluginFrameController) frame(c *gin.Context) {
	entry, user, ok := a.svc.VerifyFrameToken(c.Param("token"))
	path := c.Param("path")
	if !ok || !plugins.IsPagePath(path) {
		c.Status(http.StatusNotFound)
		return
	}
	pluginProxyError(c, a.svc.Proxy(c.Writer, c.Request, entry.ID, path, user))
}
