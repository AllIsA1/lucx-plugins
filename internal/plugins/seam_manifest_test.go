// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import (
	"strings"
	"testing"
)

func seamManifest(t *testing.T, perms []string, seams []map[string]any, mutate ...func(m map[string]any)) []byte {
	t.Helper()
	return manifestJSON(t, func(m map[string]any) {
		m["permissions"] = perms
		m["seams"] = seams
		for _, f := range mutate {
			f(m)
		}
	})
}

func TestManifestDeclaresSeamsAndNeedsThePermissionForEach(t *testing.T) {
	ok := []struct {
		name  string
		perms []string
		seams []map[string]any
	}{
		{"an exact api route", []string{"seam:api:clients"}, []map[string]any{{"engine": "api", "mode": "after", "methods": []string{"GET"}, "route": "/clients/list"}}},
		{"all methods by default", []string{"seam:api:clients"}, []map[string]any{{"engine": "api", "mode": "before", "route": "/clients/add"}}},
		{"a whole group", []string{"seam:api:inbounds"}, []map[string]any{{"engine": "api", "mode": "after", "route": "/inbounds/*"}}},
		{"the subscription server", []string{"seam:sub"}, []map[string]any{{"engine": "sub", "mode": "after", "route": "/:subid"}}},
		{"everything in after mode", []string{"seam:api:*"}, []map[string]any{{"engine": "api", "mode": "after", "route": "*"}}},
		{"replacing one route", []string{"seam:api:clients"}, []map[string]any{{"engine": "api", "mode": "replace", "methods": []string{"GET"}, "route": "/clients/list"}}},
		{"several seams", []string{"seam:api:clients", "seam:sub"}, []map[string]any{
			{"engine": "api", "mode": "after", "route": "/clients/list"}, {"engine": "sub", "mode": "after", "route": "/:subid"},
		}},
	}
	for _, c := range ok {
		t.Run("accepts "+c.name, func(t *testing.T) {
			m, err := ParseManifest(seamManifest(t, c.perms, c.seams))
			if err != nil {
				t.Fatalf("ParseManifest: %v", err)
			}
			if len(m.Seams) != len(c.seams) {
				t.Errorf("seams = %+v", m.Seams)
			}
		})
	}

	bad := []struct {
		name  string
		perms []string
		seams []map[string]any
		want  string
	}{
		{"no permission at all", nil, []map[string]any{{"engine": "api", "mode": "after", "route": "/clients/list"}}, "seam:api:clients"},
		{"the permission of another group", []string{"seam:api:inbounds"}, []map[string]any{{"engine": "api", "mode": "after", "route": "/clients/list"}}, "seam:api:clients"},
		{"a group permission for everything", []string{"seam:api:clients"}, []map[string]any{{"engine": "api", "mode": "after", "route": "*"}}, "seam:api:*"},
		{"the sub permission for an api route", []string{"seam:sub"}, []map[string]any{{"engine": "api", "mode": "after", "route": "/clients/list"}}, "seam:api:clients"},
		{"an unknown engine", []string{"seam:sub"}, []map[string]any{{"engine": "ws", "mode": "after", "route": "/x"}}, "engine"},
		{"an unknown mode", []string{"seam:sub"}, []map[string]any{{"engine": "sub", "mode": "around", "route": "/:subid"}}, "mode"},
		{"an unknown method", []string{"seam:sub"}, []map[string]any{{"engine": "sub", "mode": "after", "methods": []string{"TRACE"}, "route": "/:subid"}}, "method"},
		{"replace on everything", []string{"seam:api:*"}, []map[string]any{{"engine": "api", "mode": "replace", "route": "*"}}, "replace"},
		{"a route that is not clean", []string{"seam:api:clients"}, []map[string]any{{"engine": "api", "mode": "after", "route": "/clients/../server/getDb"}}, "route"},
		{"a route without a slash", []string{"seam:api:clients"}, []map[string]any{{"engine": "api", "mode": "after", "route": "clients/list"}}, "route"},
		{"a wildcard in the middle", []string{"seam:api:clients"}, []map[string]any{{"engine": "api", "mode": "after", "route": "/clients/*/list"}}, "route"},
		{"an empty route", []string{"seam:api:clients"}, []map[string]any{{"engine": "api", "mode": "after", "route": ""}}, "route"},
		{"a duplicate seam", []string{"seam:api:clients"}, []map[string]any{
			{"engine": "api", "mode": "after", "route": "/clients/list"}, {"engine": "api", "mode": "after", "route": "/clients/list"},
		}, "twice"},
	}
	for _, c := range bad {
		t.Run("rejects "+c.name, func(t *testing.T) {
			_, err := ParseManifest(seamManifest(t, c.perms, c.seams))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want one mentioning %q", err, c.want)
			}
		})
	}
}

func TestSeamsAreRefusedWhereTheyCannotWork(t *testing.T) {
	seam := []map[string]any{{"engine": "sub", "mode": "after", "route": "/:subid"}}
	if _, err := ParseManifest(seamManifest(t, []string{"seam:sub"}, seam, func(m map[string]any) {
		m["type"] = "ui"
		delete(m, "run")
	})); err == nil {
		t.Error("a ui plugin (no process) declared a seam")
	}
	if _, err := ParseManifest(seamManifest(t, []string{"seam:sub"}, seam, func(m map[string]any) { m["type"] = "hook" })); err == nil {
		t.Error("a hook plugin (runs only on install) declared a seam")
	}
	many := make([]map[string]any, 0, maxSeams+1)
	for i := 0; i <= maxSeams; i++ {
		many = append(many, map[string]any{"engine": "api", "mode": "after", "route": "/clients/r" + strings.Repeat("a", i%5) + string(rune('a'+i%26)) + string(rune('a'+i/26))})
	}
	if _, err := ParseManifest(seamManifest(t, []string{"seam:api:clients"}, many)); err == nil {
		t.Errorf("more than %d seams were accepted", maxSeams)
	}
}

func TestProtectedRoutesCannotBeInterceptedWhateverIsGranted(t *testing.T) {
	protected := []string{
		"/setting/updateUser", "/setting/all", "/setting/apiTokens/create", "/setting/restartPanel",
		"/server/importDB", "/server/getDb", "/server/updatePanel",
		"/nodes/mtls/ca", "/nodes/mtls/trustCA", "/nodes/updatePanel",
		"/plugins/list", "/plugins/install", "/plugins/x/:id/*path", "/lucx/hello", "/lucx/plugin-relay/:nodeId",
		"/backuptotgbot",
	}
	for _, route := range protected {
		if !IsProtectedSeamRoute(SeamEngineAPI, route) {
			t.Errorf("api route %s is not protected", route)
		}
		perms := []string{"seam:api:*", "seam:api:setting", "seam:api:server", "seam:api:nodes", "seam:api:plugins", "seam:api:lucx"}
		m, err := ParseManifest(seamManifest(t, perms, []map[string]any{{"engine": "api", "mode": "after", "route": route}}))
		if err == nil {
			t.Errorf("a seam on the protected route %s was accepted: %+v", route, m.Seams)
		}
	}
	for _, route := range []string{"/clients/list", "/inbounds/add", "/server/status", "/nodes/list", "/hosts/list", "/xray/update"} {
		if IsProtectedSeamRoute(SeamEngineAPI, route) {
			t.Errorf("%s must be open to seams", route)
		}
	}
	if IsProtectedSeamRoute(SeamEngineSub, "/:subid") {
		t.Error("the subscription route is protected")
	}
}

func TestAWildcardNeverReachesAProtectedRoute(t *testing.T) {
	// "*" and "/server/*" are allowed to be declared; at run time they must skip protected routes.
	for _, c := range []struct {
		seam, route string
		want        bool
	}{
		{"*", "/clients/list", true},
		{"*", "/setting/updateUser", false},
		{"*", "/plugins/list", false},
		{"/server/*", "/server/status", true},
		{"/server/*", "/server/getDb", false},
		{"/clients/list", "/clients/list", true},
		{"/clients/list", "/clients/add", false},
		{"/clients/*", "/clients/groups/create", true},
		{"/clients/*", "/clientsX/list", false},
		{"/clients/*", "/clients", false},
	} {
		if got := SeamRouteMatches(SeamEngineAPI, c.seam, c.route); got != c.want {
			t.Errorf("SeamRouteMatches(%q, %q) = %v, want %v", c.seam, c.route, got, c.want)
		}
	}
}

func TestSeamPermissionsAreValidDangerousAndNeedAProcess(t *testing.T) {
	for _, p := range []string{"seam:api:clients", "seam:api:inbounds", "seam:api:xray", "seam:api:*", "seam:sub"} {
		if err := ValidatePermission(p); err != nil {
			t.Errorf("%s: %v", p, err)
		}
		if !IsDangerousPermission(p) {
			t.Errorf("%s is not marked dangerous: it lets a plugin see and change what flows through the panel", p)
		}
		if !needsProcess(p) {
			t.Errorf("%s does not require a process", p)
		}
	}
	for _, p := range []string{"seam:", "seam:api:", "seam:api:setting", "seam:api:plugins", "seam:api:lucx", "seam:api:Clients", "seam:sub:x", "seam:ws", "seam:api:clients/list"} {
		if err := ValidatePermission(p); err == nil {
			t.Errorf("%q was accepted", p)
		}
	}
}

func TestAProtectedRouteIsRefusedEvenWithEveryPermissionThatIsValid(t *testing.T) {
	cases := []struct {
		route string
		perms []string
	}{
		{"/server/importDB", []string{"seam:api:server"}},
		{"/server/getDb", []string{"seam:api:server"}},
		{"/server/updatePanel", []string{"seam:api:server"}},
		{"/nodes/mtls/ca", []string{"seam:api:nodes"}},
		{"/nodes/updatePanel", []string{"seam:api:nodes"}},
		{"/backuptotgbot", []string{"seam:api:backuptotgbot"}},
	}
	for _, c := range cases {
		_, err := ParseManifest(seamManifest(t, c.perms, []map[string]any{{"engine": "api", "mode": "after", "route": c.route}}))
		if err == nil || !strings.Contains(err.Error(), "protected") {
			t.Errorf("%s with %v: err = %v, want it refused as protected", c.route, c.perms, err)
		}
	}
	if _, err := ParseManifest(seamManifest(t, []string{"seam:api:server"}, []map[string]any{{"engine": "api", "mode": "after", "route": "/server/*"}})); err != nil {
		t.Errorf("a group wildcard over a group that has protected routes must be declarable (they are skipped at run time): %v", err)
	}
}

func TestProtectionIsAboutThePanelsAPIOnly(t *testing.T) {
	// A subscription path that happens to look like a protected API group is still just a subscription path.
	for _, route := range []string{"/setting/x", "/plugins/x", "/:subid", "/json/:subid"} {
		if IsProtectedSeamRoute(SeamEngineSub, route) {
			t.Errorf("sub route %s is protected", route)
		}
	}
	if SeamRouteMatches(SeamEngineAPI, "/clients/*", "/clients/") {
		t.Error("a group wildcard matched the bare group prefix")
	}
	s := Seam{Engine: "api", Mode: "after", Methods: []string{"GET"}, Route: "/clients/list"}
	if !s.SeamMethodMatches("GET") || !s.SeamMethodMatches("HEAD") || s.SeamMethodMatches("POST") {
		t.Error("method matching: GET seams take GET and HEAD, not POST")
	}
	if !(Seam{}).SeamMethodMatches("DELETE") {
		t.Error("a seam without methods must take every method")
	}
}
