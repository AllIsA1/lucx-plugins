// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/AllIsA1/lucx-plugins/internal/hostupdate"
	"github.com/AllIsA1/lucx-plugins/internal/plugins"
)

const (
	pluginPreviewTimeout  = 3 * time.Minute
	pluginRestartTimeout  = 30 * time.Second
	defaultPluginLogLines = 200
	// pluginSlack is added to a handler's own timeout when lifting the server's write deadline.
	pluginSlack = 10 * time.Second
)

// PluginsController serves /panel/api/plugins/*: list, preview, install,
// enable/disable, uninstall and the archive-mirror setting.
type PluginsController struct {
	svc *PluginService
}

// NewPluginsController registers the plugin routes on g.
func NewPluginsController(g *gin.RouterGroup, svc *PluginService) *PluginsController {
	a := &PluginsController{svc: svc}
	g.Use(callerContext(), a.masterGate)
	a.registerNodeRoutes(g)
	g.GET("/list", a.list)
	g.GET("/menu", a.menu)
	g.GET("/catalog", a.catalog)
	g.POST("/catalog/refresh", a.catalogRefresh)
	g.POST("/catalog/preview/:id", a.catalogPreview)
	g.POST("/catalog/update-preview/:id", a.catalogUpdatePreview)
	g.POST("/catalog/settings", a.catalogSettings)
	g.GET("/frame-token/:id", a.frameToken)
	g.POST("/preview", a.preview)
	g.POST("/install", a.install)
	g.POST("/remote/install", a.remoteInstall)
	g.POST("/master-management", a.masterManagement)
	g.POST("/enable/:id", a.enable)
	g.POST("/disable/:id", a.disable)
	g.POST("/restart/:id", a.restart)
	g.POST("/update-preview/:id", a.updatePreview)
	g.POST("/update/:id", a.update)
	g.POST("/rollback/:id", a.rollback)
	g.GET("/logs/:id", a.logs)
	g.GET("/system", a.systemStatus)
	g.POST("/system/check", a.systemCheck)
	g.POST("/system/update", a.systemUpdate)
	g.GET("/x/:id/*path", a.proxy)
	g.POST("/x/:id/*path", a.proxy)
	g.PUT("/x/:id/*path", a.proxy)
	g.DELETE("/x/:id/*path", a.proxy)
	g.POST("/uninstall/:id", a.uninstall)
	g.POST("/settings", a.settings)
	return a
}

// done answers a call without a payload. Success must carry an empty msg,
// since the UI turns any non-empty success msg into a toast.
func done(c *gin.Context, failMsg string, err error) {
	if err != nil {
		jsonMsg(c, failMsg, err)
		return
	}
	jsonMsg(c, "", nil)
}

// masterGate lets a master panel (node-sync scope) use only plugins.AllowMasterRoute, and only
// once the node owner opted in. Admin sessions and tokens pass untouched.
func (a *PluginsController) masterGate(c *gin.Context) {
	if c.GetString("api_token_scope") != scopeNodeSync {
		c.Next()
		return
	}
	if !plugins.AllowMasterRoute(c.Request.Method, relAPIPath(c.FullPath())) {
		pureJsonMsg(c, http.StatusForbidden, false, "this plugin route is not available to a master panel")
		c.Abort()
		return
	}
	if !a.svc.AllowMasterManagement() {
		pureJsonMsg(c, http.StatusForbidden, false, "plugin management by the master is disabled on this node; turn it on in this node's Plugins page")
		c.Abort()
		return
	}
	c.Next()
}

// onlyMasterOwned stops a master from touching plugins the node owner installed.
func (a *PluginsController) onlyMasterOwned(c *gin.Context) bool {
	if c.GetString("api_token_scope") == scopeNodeSync && !a.svc.IsManagedByMaster(c.Param("id")) {
		pureJsonMsg(c, http.StatusForbidden, false, "this plugin was installed locally on the node and is not managed by the master")
		return false
	}
	return true
}

func (a *PluginsController) remoteInstall(c *gin.Context) {
	var body struct {
		URL            string   `json:"url"`
		Commit         string   `json:"commit"`
		ManifestSHA256 string   `json:"manifestSha256"`
		Permissions    []string `json:"permissions"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		jsonMsg(c, "plugins: invalid install body", err)
		return
	}
	extendWriteDeadline(c, pluginPreviewTimeout+pluginSlack)
	// The master gives up after 10 s; the download it started must still finish.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), pluginPreviewTimeout)
	defer cancel()
	entry, err := a.svc.InstallPinned(ctx, plugins.PinnedInstall{
		URL: body.URL, Commit: body.Commit, ManifestSHA256: body.ManifestSHA256, Permissions: body.Permissions,
	})
	if err != nil {
		jsonMsg(c, "plugins: install failed", err)
		return
	}
	jsonObj(c, entry, nil)
}

func (a *PluginsController) masterManagement(c *gin.Context) {
	var body struct {
		Allow bool `json:"allow"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		jsonMsg(c, "plugins: invalid body", err)
		return
	}
	done(c, "plugins: cannot change the setting", a.svc.SetMasterManagement(body.Allow))
}

func (a *PluginsController) updatePreview(c *gin.Context) {
	var body struct {
		Ref string `json:"ref"`
	}
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&body); err != nil {
			jsonMsg(c, "plugins: invalid update body", err)
			return
		}
	}
	extendWriteDeadline(c, pluginPreviewTimeout+pluginSlack)
	ctx, cancel := context.WithTimeout(c.Request.Context(), pluginPreviewTimeout)
	defer cancel()
	up, err := a.svc.PreviewUpdate(ctx, c.Param("id"), body.Ref)
	if err != nil {
		jsonMsg(c, "plugins: cannot check for an update", err)
		return
	}
	jsonObj(c, up, nil)
}

func (a *PluginsController) update(c *gin.Context) {
	var body struct {
		Token       string   `json:"token"`
		Permissions []string `json:"permissions"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		jsonMsg(c, "plugins: invalid update body", err)
		return
	}
	extendWriteDeadline(c, pluginPreviewTimeout+pluginSlack)
	// A browser that gives up must not leave a half-done update behind.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), pluginPreviewTimeout)
	defer cancel()
	entry, err := a.svc.ApplyUpdate(ctx, c.Param("id"), body.Token, body.Permissions)
	if err != nil {
		jsonMsg(c, "plugins: update failed", err)
		return
	}
	jsonObj(c, entry, nil)
}

func (a *PluginsController) rollback(c *gin.Context) {
	extendWriteDeadline(c, pluginRestartTimeout+pluginSlack)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), pluginRestartTimeout)
	defer cancel()
	entry, err := a.svc.Rollback(ctx, c.Param("id"))
	if err != nil {
		jsonMsg(c, "plugins: rollback failed", err)
		return
	}
	jsonObj(c, entry, nil)
}

func (a *PluginsController) catalogView(c *gin.Context, force bool) {
	extendWriteDeadline(c, pluginPreviewTimeout)
	ctx, cancel := context.WithTimeout(c.Request.Context(), pluginRestartTimeout)
	defer cancel()
	jsonObj(c, a.svc.Catalog(ctx, force), nil)
}

func (a *PluginsController) catalog(c *gin.Context) { a.catalogView(c, false) }

func (a *PluginsController) catalogRefresh(c *gin.Context) { a.catalogView(c, true) }

func (a *PluginsController) catalogPreview(c *gin.Context) {
	extendWriteDeadline(c, pluginPreviewTimeout+pluginSlack)
	ctx, cancel := context.WithTimeout(c.Request.Context(), pluginPreviewTimeout)
	defer cancel()
	p, err := a.svc.PreviewCatalog(ctx, c.Param("id"))
	if err != nil {
		jsonMsg(c, "plugins: cannot preview the catalog plugin", err)
		return
	}
	jsonObj(c, p, nil)
}

func (a *PluginsController) catalogUpdatePreview(c *gin.Context) {
	extendWriteDeadline(c, pluginPreviewTimeout+pluginSlack)
	ctx, cancel := context.WithTimeout(c.Request.Context(), pluginPreviewTimeout)
	defer cancel()
	up, err := a.svc.PreviewCatalogUpdate(ctx, c.Param("id"))
	if err != nil {
		jsonMsg(c, "plugins: cannot check the catalog for an update", err)
		return
	}
	jsonObj(c, up, nil)
}

func (a *PluginsController) catalogSettings(c *gin.Context) {
	var body struct {
		Base string `json:"base"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		jsonMsg(c, "plugins: invalid settings body", err)
		return
	}
	done(c, "plugins: catalog settings save failed", a.svc.SetCatalogBase(body.Base))
}

func (a *PluginsController) frameToken(c *gin.Context) {
	user := loginUser(c)
	token, expires, err := a.svc.FrameToken(c.Param("id"), user)
	if err != nil {
		jsonMsg(c, "plugins: no page token", err)
		return
	}
	jsonObj(c, map[string]any{"token": token, "expiresAt": expires}, nil)
}

func (a *PluginsController) menu(c *gin.Context) {
	jsonObj(c, a.svc.UI(), nil)
}

func (a *PluginsController) list(c *gin.Context) {
	jsonObj(c, a.svc.State(), nil)
}

func (a *PluginsController) preview(c *gin.Context) {
	var body struct {
		URL string `json:"url"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		jsonMsg(c, "plugins: invalid preview body", err)
		return
	}
	extendWriteDeadline(c, pluginPreviewTimeout+pluginSlack)
	ctx, cancel := context.WithTimeout(c.Request.Context(), pluginPreviewTimeout)
	defer cancel()
	p, err := a.svc.Preview(ctx, body.URL)
	if err != nil {
		jsonMsg(c, "plugins: preview failed", err)
		return
	}
	jsonObj(c, p, nil)
}

func (a *PluginsController) install(c *gin.Context) {
	var body struct {
		Token       string   `json:"token"`
		Permissions []string `json:"permissions"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		jsonMsg(c, "plugins: invalid install body", err)
		return
	}
	entry, err := a.svc.Install(body.Token, body.Permissions)
	if err != nil {
		jsonMsg(c, "plugins: install failed", err)
		return
	}
	jsonObj(c, entry, nil)
}

func (a *PluginsController) enable(c *gin.Context) {
	if !a.onlyMasterOwned(c) {
		return
	}
	done(c, "plugins: enable failed", a.svc.SetEnabled(c.Param("id"), true))
}

func (a *PluginsController) disable(c *gin.Context) {
	if !a.onlyMasterOwned(c) {
		return
	}
	done(c, "plugins: disable failed", a.svc.SetEnabled(c.Param("id"), false))
}

// proxy hands the request to the plugin process; the panel has already
// authenticated the caller and checked CSRF, the plugin never sees either.
func (a *PluginsController) proxy(c *gin.Context) {
	user := loginUser(c)
	pluginProxyError(c, a.svc.Proxy(c.Writer, c.Request, c.Param("id"), c.Param("path"), user))
}

// pluginProxyError answers a failed plugin request; nil means the plugin already answered.
func pluginProxyError(c *gin.Context, err error) {
	switch {
	case err == nil:
	case errors.Is(err, plugins.ErrNotFound), errors.Is(err, plugins.ErrReservedPath):
		pureJsonMsg(c, http.StatusNotFound, false, "plugin endpoint not found")
	case errors.Is(err, plugins.ErrNotRunning):
		pureJsonMsg(c, http.StatusServiceUnavailable, false, "the plugin is not running")
	case errors.Is(err, plugins.ErrMethodNotAllowed):
		pureJsonMsg(c, http.StatusMethodNotAllowed, false, err.Error())
	case errors.Is(err, plugins.ErrUpgradeUnsupported):
		pureJsonMsg(c, http.StatusBadRequest, false, err.Error())
	default:
		pureJsonMsg(c, http.StatusInternalServerError, false, err.Error())
	}
}

func (a *PluginsController) restart(c *gin.Context) {
	if !a.onlyMasterOwned(c) {
		return
	}
	extendWriteDeadline(c, pluginRestartTimeout+pluginSlack)
	ctx, cancel := context.WithTimeout(c.Request.Context(), pluginRestartTimeout)
	defer cancel()
	done(c, "plugins: restart failed", a.svc.Restart(ctx, c.Param("id")))
}

func (a *PluginsController) logs(c *gin.Context) {
	if !a.onlyMasterOwned(c) {
		return
	}
	lines, ok := logLines(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), pluginRestartTimeout)
	defer cancel()
	out, err := a.svc.Logs(ctx, c.Param("id"), lines)
	if err != nil {
		jsonMsg(c, "plugins: cannot read the log", err)
		return
	}
	jsonObj(c, out, nil)
}

func (a *PluginsController) uninstall(c *gin.Context) {
	if !a.onlyMasterOwned(c) {
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
	done(c, "plugins: uninstall failed", a.svc.Uninstall(c.Param("id"), body.RemoveData))
}

func (a *PluginsController) settings(c *gin.Context) {
	var body struct {
		ArchiveBase string `json:"archiveBase"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		jsonMsg(c, "plugins: invalid settings body", err)
		return
	}
	done(c, "plugins: settings save failed", a.svc.SetArchiveBase(body.ArchiveBase))
}

// logLines reads ?lines=, answering the request itself when it is invalid.
func logLines(c *gin.Context) (int, bool) {
	raw := c.Query("lines")
	if raw == "" {
		return defaultPluginLogLines, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		jsonMsg(c, "plugins: invalid lines parameter", errors.New("lines must be a positive number"))
		return 0, false
	}
	return n, true
}

// extendWriteDeadline lets this response take up to d: the server's WriteTimeout would otherwise cut it.
func extendWriteDeadline(c *gin.Context, d time.Duration) {
	var w http.ResponseWriter = c.Writer
	if raw, ok := c.Get(rawWriterKey); ok {
		if rw, ok := raw.(http.ResponseWriter); ok {
			w = rw
		}
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(d))
}

// restartDelay lets the answer to the update request leave before the host stops serving.
const restartDelay = 400 * time.Millisecond

func (a *PluginsController) systemStatus(c *gin.Context) {
	if a.svc.Updater == nil {
		jsonObj(c, hostupdate.Status{}, nil)
		return
	}
	jsonObj(c, a.svc.Updater.Status(), nil)
}

func (a *PluginsController) systemCheck(c *gin.Context) {
	if a.svc.Updater == nil {
		jsonMsg(c, "plugins: this plugin system build does not update itself", errors.New("not managed"))
		return
	}
	extendWriteDeadline(c, pluginRestartTimeout+pluginSlack)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), pluginRestartTimeout)
	defer cancel()
	done(c, "plugins: cannot check for a newer plugin system", a.svc.Updater.Check(ctx))
}

func (a *PluginsController) systemUpdate(c *gin.Context) {
	if a.svc.Updater == nil {
		jsonMsg(c, "plugins: this plugin system build does not update itself", errors.New("not managed"))
		return
	}
	extendWriteDeadline(c, pluginPreviewTimeout+pluginSlack)
	// A browser that gives up must not leave a half-installed binary behind.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), pluginPreviewTimeout)
	defer cancel()
	if err := a.svc.Updater.Apply(ctx); err != nil {
		jsonMsg(c, "plugins: cannot update the plugin system", err)
		return
	}
	done(c, "", nil)
	if a.svc.RestartHost != nil {
		time.AfterFunc(restartDelay, a.svc.RestartHost)
	}
}
