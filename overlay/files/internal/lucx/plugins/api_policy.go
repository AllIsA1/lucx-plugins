// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import "net/http"

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
	"api:xray:write":     post("/xray/update", "/xray/resetOutboundsTraffic"),
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

// APIPolicyRoutes lists every route some permission can grant; tests check
// each one still exists in the router.
func APIPolicyRoutes() []APIRoute {
	var out []APIRoute
	for _, rs := range apiPolicy {
		out = append(out, rs...)
	}
	return out
}
