// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import (
	"net/http"
	"testing"
)

func TestMasterRoutesAreTheNarrowSetANodeOffers(t *testing.T) {
	allowed := []struct{ method, pattern string }{
		{"GET", "/plugins/list"},
		{"POST", "/plugins/remote/install"},
		{"POST", "/plugins/enable/:id"},
		{"POST", "/plugins/disable/:id"},
		{"POST", "/plugins/uninstall/:id"},
		{"POST", "/plugins/restart/:id"},
		{"GET", "/plugins/logs/:id"},
		{"HEAD", "/plugins/list"},
	}
	for _, r := range allowed {
		if !AllowMasterRoute(r.method, r.pattern) {
			t.Errorf("%s %s must be available to a master", r.method, r.pattern)
		}
	}
	denied := []struct{ method, pattern string }{
		{"POST", "/plugins/preview"},
		{"POST", "/plugins/install"},
		{"POST", "/plugins/settings"},
		{"POST", "/plugins/master-management"},
		{"GET", "/plugins/x/:id/*path"},
		{"POST", "/plugins/x/:id/*path"},
		{"DELETE", "/plugins/x/:id/*path"},
		{"GET", "/plugins/remote/install"},
		{"GET", "/plugins/enable/:id"},
		{"POST", "/plugins/list"},
		{"POST", "/plugins/enable/alerts"},
		{"POST", "/server/updatePanel"},
		{"GET", "/clients/list"},
		{http.MethodPut, "/plugins/enable/:id"},
	}
	for _, r := range denied {
		if AllowMasterRoute(r.method, r.pattern) {
			t.Errorf("%s %s must not be available to a master: it changes or reaches into more than the node owner agreed to", r.method, r.pattern)
		}
	}
}
