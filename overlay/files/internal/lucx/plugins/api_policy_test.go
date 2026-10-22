// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import (
	"net/http"
	"slices"
	"strings"
	"testing"
)

func allAPIPermissions() []string {
	var out []string
	for perm := range apiPolicy {
		out = append(out, perm)
	}
	slices.Sort(out)
	return out
}

func TestAllowAPIFollowsThePermissionsHeld(t *testing.T) {
	read := []string{"api:clients:read"}
	cases := []struct {
		perms   []string
		method  string
		pattern string
		want    bool
	}{
		{read, http.MethodGet, "/clients/list", true},
		{read, http.MethodHead, "/clients/list", true},
		{read, http.MethodPost, "/clients/onlines", true},
		{read, http.MethodPost, "/clients/add", false},
		{read, http.MethodGet, "/inbounds/list", false},
		{[]string{"api:clients:write"}, http.MethodPost, "/clients/add", true},
		{[]string{"api:clients:write"}, http.MethodGet, "/clients/list", false},
		{[]string{"api:inbounds:read", "api:clients:read"}, http.MethodGet, "/inbounds/list", true},
		{nil, http.MethodGet, "/clients/list", false},
		{[]string{"events:client.created", "network", "ui:menu"}, http.MethodGet, "/clients/list", false},
		{read, http.MethodGet, "/clients/get/:email", true},
		{read, http.MethodGet, "/clients/get/alice@example.com", false},
		{read, http.MethodDelete, "/clients/list", false},
	}
	for _, tc := range cases {
		if got := AllowAPI(tc.perms, tc.method, tc.pattern); got != tc.want {
			t.Errorf("AllowAPI(%v, %s %s) = %v, want %v", tc.perms, tc.method, tc.pattern, got, tc.want)
		}
	}
}

// Even a plugin holding every api permission must not reach routes that hand
// over the database, rotate credentials or run code as root.
func TestAllowAPINeverReachesPrivilegedRoutes(t *testing.T) {
	all := allAPIPermissions()
	forbidden := []struct{ method, pattern string }{
		{"GET", "/server/getDb"},
		{"POST", "/server/importDB"},
		{"GET", "/server/getConfigJson"},
		{"POST", "/server/updatePanel"},
		{"POST", "/server/installXray/:version"},
		{"POST", "/server/rebuildAwgModule"},
		{"POST", "/server/uninstallAwgModule"},
		{"POST", "/setting/updateUser"},
		{"POST", "/setting/restartPanel"},
		{"POST", "/setting/factoryDefaults"},
		{"GET", "/setting/apiTokens"},
		{"POST", "/setting/apiTokens/create"},
		{"POST", "/setting/apiTokens/delete/:id"},
		{"POST", "/setting/apiTokens/setEnabled/:id"},
		{"POST", "/backuptotgbot"},
		{"GET", "/plugins/list"},
		{"POST", "/plugins/install"},
		{"POST", "/plugins/enable/:id"},
		{"POST", "/plugins/x/:id/*path"},
		{"POST", "/tunnel/naive/upload"},
		{"POST", "/sidecar-outbounds/upload/:protocol"},
		{"POST", "/clients/import"},
		{"GET", "/clients/export"},
		{"POST", "/inbounds/import"},
		{"POST", "/clients/resetAllTraffics"},
		{"POST", "/inbounds/resetAllTraffics"},
		{"POST", "/nodes/mtls/ca"},
		{"POST", "/nodes/updatePanel"},
	}
	for _, f := range forbidden {
		if AllowAPI(all, f.method, f.pattern) {
			t.Errorf("a plugin with every api permission can call %s %s", f.method, f.pattern)
		}
	}
}

func TestAPIPolicyIsWellFormed(t *testing.T) {
	prefix := map[string]string{
		"clients": "/clients", "inbounds": "/inbounds", "hosts": "/hosts", "nodes": "/nodes",
		"server": "/server", "xray": "/xray", "settings": "/setting",
	}
	seen := map[string]string{}
	for perm, routes := range apiPolicy {
		if err := ValidatePermission(perm); err != nil {
			t.Errorf("policy key %q is not a valid permission", perm)
		}
		parts := strings.Split(perm, ":")
		want := prefix[parts[1]]
		isRead := parts[2] == "read"
		for _, r := range routes {
			if !strings.HasPrefix(r.Pattern, want+"/") && r.Pattern != want {
				t.Errorf("%s lists %s %s outside its resource %s", perm, r.Method, r.Pattern, want)
			}
			if r.Method != http.MethodGet && r.Method != http.MethodPost {
				t.Errorf("%s lists unsupported method %s", perm, r.Method)
			}
			if r.Method == http.MethodGet && !isRead {
				t.Errorf("%s lists the GET %s: reads belong to the read permission", perm, r.Pattern)
			}
			key := r.Method + " " + r.Pattern
			if other, dup := seen[key]; dup {
				t.Errorf("%s is granted by both %s and %s", key, other, perm)
			}
			seen[key] = perm
		}
	}
	for resource := range prefix {
		for _, mode := range []string{"read", "write"} {
			if _, ok := apiPolicy["api:"+resource+":"+mode]; !ok {
				t.Errorf("no policy for api:%s:%s although the grammar accepts it", resource, mode)
			}
		}
	}
}

func TestSensitiveAPIPermissionsAreDangerous(t *testing.T) {
	for _, p := range []string{"api:settings:read", "api:settings:write", "api:nodes:read", "api:nodes:write", "api:clients:write"} {
		if !IsDangerousPermission(p) {
			t.Errorf("%s exposes secrets or controls other servers and must be flagged", p)
		}
	}
	for _, p := range []string{"api:clients:read", "api:inbounds:read", "api:server:read", "api:hosts:read", "api:xray:read"} {
		if IsDangerousPermission(p) {
			t.Errorf("%s is an ordinary read permission", p)
		}
	}
}
