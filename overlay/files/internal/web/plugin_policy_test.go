// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package web

import (
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/lucx/plugins"
)

// A permission that names a route the router no longer has would silently grant
// nothing; one that drifted to a renamed route would be a hole in the other direction.
func TestPluginPolicyRoutesExistInTheRouter(t *testing.T) {
	registered := registeredContractRoutes(t)
	for _, r := range plugins.APIPolicyRoutes() {
		if !registered[r.Method+" /panel/api"+r.Pattern] {
			t.Errorf("plugin policy grants %s %s but the router has no such route", r.Method, r.Pattern)
		}
	}
}

func TestMasterRoutesExistInTheRouter(t *testing.T) {
	registered := registeredContractRoutes(t)
	for _, r := range plugins.MasterRoutes() {
		if !registered[r.Method+" /panel/api"+r.Pattern] {
			t.Errorf("master route %s %s is not registered", r.Method, r.Pattern)
		}
	}
}
