// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostapi

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// registerNodeRoutes adds the master's per-node plugin commands. None of them
// is in the master allowlist, so a node can never use them on its peers.
func (a *PluginsController) registerNodeRoutes(g *gin.RouterGroup) {
	g.Use(func(c *gin.Context) {
		if a.svc.Nodes == nil && len(c.Request.URL.Path) > 0 && isNodeRoute(c.FullPath()) {
			jsonMsg(c, "plugins: node management is not available", errors.New("this plugin system has no link to the panel's nodes"))
			c.Abort()
			return
		}
		c.Next()
	})
	g.GET("/nodes", a.nodeList)
	g.POST("/nodes/:nodeId/refresh", a.nodeRefresh)
	g.POST("/nodes/:nodeId/install", a.nodeInstall)
	g.POST("/nodes/:nodeId/install-link", a.nodeInstallLink)
	g.POST("/nodes/:nodeId/update/:id", a.nodeUpdate)
	g.POST("/nodes/:nodeId/enable/:id", a.nodeEnable)
	g.POST("/nodes/:nodeId/disable/:id", a.nodeDisable)
	g.POST("/nodes/:nodeId/restart/:id", a.nodeRestart)
	g.POST("/nodes/:nodeId/uninstall/:id", a.nodeUninstall)
	g.GET("/nodes/:nodeId/logs/:id", a.nodeLogs)
}

func nodeIDParam(c *gin.Context) (int, bool) {
	id, err := strconv.Atoi(c.Param("nodeId"))
	if err != nil || id < 1 {
		jsonMsg(c, "plugins: invalid node id", errors.New("the node id must be a positive number"))
		return 0, false
	}
	return id, true
}

// nodeCtx outlives the browser's request on purpose: a node install keeps working after a hang-up.
func nodeCtx(c *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(c.Request.Context()), pluginRestartTimeout)
}

func (a *PluginsController) nodeList(c *gin.Context) {
	out, err := a.nodes().Statuses(c.Request.Context())
	jsonObj(c, out, err)
}

func (a *PluginsController) nodeRefresh(c *gin.Context) {
	id, ok := nodeIDParam(c)
	if !ok {
		return
	}
	ctx, cancel := nodeCtx(c)
	defer cancel()
	done(c, "plugins: cannot read the node", a.nodes().Refresh(ctx, id))
}

func (a *PluginsController) nodeInstall(c *gin.Context) {
	id, ok := nodeIDParam(c)
	if !ok {
		return
	}
	var body struct {
		ID          string   `json:"id"`
		Permissions []string `json:"permissions"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		jsonMsg(c, "plugins: invalid install body", err)
		return
	}
	ctx, cancel := nodeCtx(c)
	defer cancel()
	done(c, "plugins: node install failed", a.nodes().Install(ctx, id, body.ID, body.Permissions))
}

// nodeInstallLink installs on a node a plugin the operator previewed by its link on this panel (POST /preview).
func (a *PluginsController) nodeInstallLink(c *gin.Context) {
	id, ok := nodeIDParam(c)
	if !ok {
		return
	}
	var body struct {
		Token       string   `json:"token"`
		Permissions []string `json:"permissions"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		jsonMsg(c, "plugins: invalid install body", err)
		return
	}
	req, err := a.svc.PinPreview(body.Token, body.Permissions)
	if err != nil {
		jsonMsg(c, "plugins: node install failed", err)
		return
	}
	ctx, cancel := nodeCtx(c)
	defer cancel()
	done(c, "plugins: node install failed", a.nodes().InstallPinned(ctx, id, req))
}

func (a *PluginsController) nodeUpdate(c *gin.Context) {
	id, ok := nodeIDParam(c)
	if !ok {
		return
	}
	var body struct {
		Permissions []string `json:"permissions"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		jsonMsg(c, "plugins: invalid update body", err)
		return
	}
	// Three node calls in a row, each up to 15 s: the server's write deadline would cut the answer.
	extendWriteDeadline(c, 3*pluginRestartTimeout)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 3*pluginRestartTimeout)
	defer cancel()
	done(c, "plugins: node update failed", a.nodes().Update(ctx, id, c.Param("id"), body.Permissions))
}

func (a *PluginsController) nodeEnable(c *gin.Context) { a.nodeSetEnabled(c, true) }

func (a *PluginsController) nodeDisable(c *gin.Context) { a.nodeSetEnabled(c, false) }

func (a *PluginsController) nodeSetEnabled(c *gin.Context, enabled bool) {
	id, ok := nodeIDParam(c)
	if !ok {
		return
	}
	ctx, cancel := nodeCtx(c)
	defer cancel()
	done(c, "plugins: node command failed", a.nodes().SetEnabled(ctx, id, c.Param("id"), enabled))
}

func (a *PluginsController) nodeRestart(c *gin.Context) {
	id, ok := nodeIDParam(c)
	if !ok {
		return
	}
	ctx, cancel := nodeCtx(c)
	defer cancel()
	done(c, "plugins: node command failed", a.nodes().Restart(ctx, id, c.Param("id")))
}

func (a *PluginsController) nodeUninstall(c *gin.Context) {
	id, ok := nodeIDParam(c)
	if !ok {
		return
	}
	var body struct {
		RemoveData bool `json:"removeData"`
	}
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&body); err != nil {
			jsonMsg(c, "plugins: invalid uninstall body", err)
			return
		}
	}
	ctx, cancel := nodeCtx(c)
	defer cancel()
	done(c, "plugins: node command failed", a.nodes().Uninstall(ctx, id, c.Param("id"), body.RemoveData))
}

func (a *PluginsController) nodeLogs(c *gin.Context) {
	id, ok := nodeIDParam(c)
	if !ok {
		return
	}
	lines, ok := logLines(c)
	if !ok {
		return
	}
	ctx, cancel := nodeCtx(c)
	defer cancel()
	out, err := a.nodes().Logs(ctx, id, c.Param("id"), lines)
	if err != nil {
		jsonMsg(c, "plugins: cannot read the node log", err)
		return
	}
	jsonObj(c, out, nil)
}

func (a *PluginsController) nodes() *NodePluginsService { return a.svc.Nodes }

func isNodeRoute(fullPath string) bool {
	return strings.Contains(fullPath, "/plugins/nodes")
}
