// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import (
	"net/http"
	"sort"
)

// APIRoute is one panel API route, as a gin pattern relative to /panel/api.
type APIRoute struct {
	Method  string
	Pattern string
}

func get(patterns ...string) []APIRoute  { return routes(http.MethodGet, patterns) }
func post(patterns ...string) []APIRoute { return routes(http.MethodPost, patterns) }

func routes(method string, patterns []string) []APIRoute {
	out := make([]APIRoute, len(patterns))
	for i, p := range patterns {
		out[i] = APIRoute{Method: method, Pattern: p}
	}
	return out
}

func join(groups ...[]APIRoute) []APIRoute {
	var out []APIRoute
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

// apiPolicy is an allowlist: a plugin reaches only routes some permission it
// holds names. Write does not imply read. Routes that hand over the database,
// rotate credentials, update or reconfigure the panel are in no list.
var apiPolicy = map[string][]APIRoute{
	"api:clients:read": join(
		get("/clients/get/:email", "/clients/get/tgId/:tgId", "/clients/list", "/clients/list/paged",
			"/clients/links/:email", "/clients/subLinks/:subId", "/clients/traffic/:email",
			"/clients/groups", "/clients/groups/:name/emails"),
		post("/clients/onlines", "/clients/lastOnline", "/clients/activeInbounds", "/clients/ips/:email",
			"/clients/renewalPreview"),
	),
	"api:clients:write": post(
		"/clients/add", "/clients/update/:email", "/clients/del/:email", "/clients/:email/attach",
		"/clients/:email/detach", "/clients/bulkAdjust", "/clients/bulkAttach", "/clients/bulkCreate",
		"/clients/bulkDel", "/clients/bulkDetach", "/clients/bulkDisable", "/clients/bulkEnable",
		"/clients/bulkResetTraffic", "/clients/resetTraffic/:email", "/clients/updateTraffic/:email",
		"/clients/clearIps/:email", "/clients/delDepleted", "/clients/groups/bulkAdd",
		"/clients/groups/bulkRemove", "/clients/groups/create", "/clients/groups/delete",
		"/clients/groups/rename",
	),
	"api:inbounds:read": get("/inbounds/list", "/inbounds/list/slim", "/inbounds/get/:id",
		"/inbounds/options", "/inbounds/:id/links", "/inbounds/allLinks"),
	"api:inbounds:write": post("/inbounds/add", "/inbounds/update/:id", "/inbounds/del/:id",
		"/inbounds/setEnable/:id", "/inbounds/:id/resetTraffic", "/inbounds/bulkDel"),
	"api:hosts:read": get("/hosts/list", "/hosts/get/:groupId", "/hosts/byInbound/:inboundId", "/hosts/tags"),
	"api:hosts:write": post("/hosts/add", "/hosts/update/:groupId", "/hosts/del/:groupId",
		"/hosts/setEnable/:groupId", "/hosts/reorder", "/hosts/bulk/add", "/hosts/bulk/del",
		"/hosts/bulk/setEnable"),
	"api:nodes:read": get("/nodes/list", "/nodes/get/:id", "/nodes/history/:id/:metric/:bucket"),
	"api:nodes:write": post("/nodes/add", "/nodes/update/:id", "/nodes/del/:id", "/nodes/setEnable/:id",
		"/nodes/probe/:id"),
	"api:server:read": join(
		get("/server/status", "/server/cpuHistory/:bucket", "/server/history/:metric/:bucket",
			"/server/xrayMetricsState", "/server/xrayMetricsHistory/:metric/:bucket",
			"/server/xrayObservatory", "/server/xrayObservatoryHistory/:tag/:bucket",
			"/server/getXrayVersion", "/server/getPanelUpdateInfo", "/server/bbrStatus",
			"/server/fail2banStatus"),
		post("/server/logs/:count", "/server/xraylogs/:count"),
	),
	"api:server:write": post("/server/restartXrayService", "/server/stopXrayService"),
	"api:xray:read": join(
		get("/xray/getOutboundsTraffic", "/xray/getXrayResult", "/xray/outbound-subs"),
		post("/xray/", "/xray/balancerStatus"),
	),
	"api:xray:write": post("/xray/update", "/xray/resetOutboundsTraffic"),
	// openflux is the OpenFlux exit's core binary and nothing else of the tunnel routes (no upload, no logs, no
	// other core): a plugin that keeps the core up to date needs to see whether it is there and to replace it by
	// a download that must match a hash it names. The panel runs that binary as root, so write is the dangerous permission it always is.
	"api:openflux:read":  get("/tunnel/openflux/status"),
	"api:openflux:write": post("/tunnel/openflux/download", "/tunnel/openflux/deleteBinary"),
	// cores: whether each tunnel core other than OpenFlux (which has its own pair) is there and running. Read only:
	// replacing a core is a download the panel runs as root, which no plugin may do for these (olcRTC has api:olcrtc:write).
	"api:cores:read": get(
		"/tunnel/naive/status", "/tunnel/olcrtc/status", "/tunnel/qwdtt/status", "/tunnel/csqtt/status", "/tunnel/mieru/status",
		"/tunnel/trusttunnel/status", "/tunnel/anytls/status", "/tunnel/tproxy/status", "/tunnel/mtproxy/status"),
	// olcrtc: replace the olcRTC core by a download that must match a hash the plugin names, and remove it. Write only:
	// whether it is there is api:cores:read (a route is granted by one permission). The panel runs the file as root.
	"api:olcrtc:write":   post("/tunnel/olcrtc/download", "/tunnel/olcrtc/deleteBinary"),
	"api:settings:read":  post("/setting/all"),
	"api:settings:write": post("/setting/update"),
}

// AllowAPI reports whether a plugin holding perms may call the route. pattern
// is the gin route pattern relative to /panel/api, never a concrete path, so
// route parameters cannot widen what a permission grants. HEAD counts as GET.
func AllowAPI(perms []string, method, pattern string) bool {
	if method == http.MethodHead {
		method = http.MethodGet
	}
	for _, perm := range perms {
		for _, r := range apiPolicy[perm] {
			if r.Method == method && r.Pattern == pattern {
				return true
			}
		}
	}
	return false
}

// AllowedRoutes lists the panel routes the permissions grant, sorted and without duplicates. The
// gateway hands it to the panel, which enforces it against the route it really resolves.
func AllowedRoutes(perms []string) []APIRoute {
	seen := map[APIRoute]bool{}
	var out []APIRoute
	for _, perm := range perms {
		for _, r := range apiPolicy[perm] {
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pattern != out[j].Pattern {
			return out[i].Pattern < out[j].Pattern
		}
		return out[i].Method < out[j].Method
	})
	return out
}

// APIPolicyRoutes lists every route some permission can grant; tests check
// each one still exists in the router.
func APIPolicyRoutes() []APIRoute {
	var out []APIRoute
	for _, rs := range apiPolicy {
		out = append(out, rs...)
	}
	return out
}
