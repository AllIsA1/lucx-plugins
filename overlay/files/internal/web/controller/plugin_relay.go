// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/lucx/hostlink"
	"github.com/mhsanaei/3x-ui/v3/internal/web/runtime"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

const (
	relayTimeout     = 15 * time.Second
	relayMaxRequest  = 1 << 20
	relayMaxLogLines = 1000
)

// relayRoute is a call a master panel may make on a node's plugin API, as a route pattern. It is
// the same list the node enforces (the plugin system's master routes); a test of the plugin system
// repository compares the two.
type relayRoute struct{ method, pattern string }

var relayRoutes = []relayRoute{
	{http.MethodGet, "/panel/api/plugins/list"},
	{http.MethodGet, "/panel/api/plugins/logs/:id"},
	{http.MethodPost, "/panel/api/plugins/remote/install"},
	{http.MethodPost, "/panel/api/plugins/remote/update"},
	{http.MethodGet, "/panel/api/plugins/system"},
	{http.MethodPost, "/panel/api/plugins/system/check"},
	{http.MethodPost, "/panel/api/plugins/system/update"},
	{http.MethodPost, "/panel/api/plugins/enable/:id"},
	{http.MethodPost, "/panel/api/plugins/disable/:id"},
	{http.MethodPost, "/panel/api/plugins/uninstall/:id"},
	{http.MethodPost, "/panel/api/plugins/restart/:id"},
}

// pluginIDSegment is what a plugin id looks like; anything else in an :id position is refused.
var pluginIDSegment = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$`)

// PluginRelayController carries the plugin system's calls to nodes. The node channel (credentials,
// TLS mode, egress) is the panel's, so the plugin system, which runs separately, asks the panel to
// make the call. Only the plugin host's socket can reach it.
type PluginRelayController struct{}

// NewPluginRelayController registers POST <g>/plugin-relay/:nodeId.
func NewPluginRelayController(g *gin.RouterGroup) *PluginRelayController {
	a := &PluginRelayController{}
	g.POST("/plugin-relay/:nodeId", a.relay)
	return a
}

type relayRequest struct {
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Body   json.RawMessage `json:"body"`
}

// relayTarget validates the requested call and returns the path to send to the node (query kept
// only where a route takes one). ok is false for anything but an exact match of a listed route.
func relayTarget(method, raw string) (target string, ok bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return "", false
	}
	if u.Path != u.EscapedPath() || strings.Contains(raw, "#") {
		return "", false // an encoded character could hide a slash or a dot segment
	}
	segments := strings.Split(u.Path, "/")
	for _, route := range relayRoutes {
		if route.method != method {
			continue
		}
		want := strings.Split(route.pattern, "/")
		if len(want) != len(segments) {
			continue
		}
		match := true
		for i := range want {
			switch {
			case strings.HasPrefix(want[i], ":"):
				match = match && pluginIDSegment.MatchString(segments[i])
			default:
				match = match && want[i] == segments[i]
			}
		}
		if !match {
			continue
		}
		query, ok := relayQuery(route.pattern, u.Query(), u.RawQuery)
		if !ok {
			return "", false
		}
		return u.Path + query, true
	}
	return "", false
}

// relayQuery allows `lines=<n>` on the logs route and nothing anywhere else.
func relayQuery(pattern string, q url.Values, raw string) (string, bool) {
	if raw == "" {
		return "", true
	}
	if pattern != "/panel/api/plugins/logs/:id" || len(q) != 1 {
		return "", false
	}
	n, err := strconv.Atoi(q.Get("lines"))
	if err != nil || n < 1 || n > relayMaxLogLines {
		return "", false
	}
	return "?lines=" + strconv.Itoa(n), true
}

func relayFail(c *gin.Context, status int, msg string) {
	c.JSON(status, gin.H{"success": false, "msg": msg, "obj": nil})
}

func (a *PluginRelayController) relay(c *gin.Context) {
	if !hostlink.FromHost(c) {
		c.Status(http.StatusNotFound) // for a browser the route does not exist, whoever is logged in
		return
	}
	nodeID, err := strconv.Atoi(c.Param("nodeId"))
	if err != nil || nodeID < 1 {
		relayFail(c, http.StatusBadRequest, "invalid node id")
		return
	}
	var req relayRequest
	if err := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, relayMaxRequest)).Decode(&req); err != nil {
		relayFail(c, http.StatusBadRequest, "invalid relay request")
		return
	}
	target, ok := relayTarget(req.Method, req.Path)
	if !ok {
		relayFail(c, http.StatusForbidden, "this call is not one a master panel may make on a node")
		return
	}
	node, err := (&service.NodeService{}).GetById(nodeID)
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		relayFail(c, http.StatusNotFound, "node not found")
		return
	case err != nil:
		relayFail(c, http.StatusInternalServerError, "cannot read the node")
		return
	case !node.Enable:
		relayFail(c, http.StatusConflict, "the node is disabled")
		return
	}
	mgr := runtime.GetManager()
	if mgr == nil {
		relayFail(c, http.StatusServiceUnavailable, "the node runtime is not ready")
		return
	}
	remote, err := mgr.RemoteFor(node)
	if err != nil {
		relayFail(c, http.StatusBadGateway, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), relayTimeout)
	defer cancel()
	status, body, err := runtime.RelayPlugin(ctx, remote, req.Method, target, req.Body)
	if err != nil {
		relayFail(c, http.StatusBadGateway, "the node did not answer: "+err.Error())
		return
	}
	c.Header("X-LucX-Relayed", "node")
	c.Data(status, "application/json; charset=utf-8", body)
}
