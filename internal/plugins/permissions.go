// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

// Package plugins is the LucX plugin system: manifests, the on-disk
// registry and the GitHub installer. It never runs plugin code itself.
package plugins

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const (
	permNetwork = "network"
	permUIMenu  = "ui:menu"
)

var (
	apiPermission      = regexp.MustCompile(`^api:((clients|inbounds|hosts|nodes|server|xray|settings|openflux):(read|write)|cores:read|olcrtc:write)$`)
	eventPermission    = regexp.MustCompile(`^events:[a-z][a-z0-9]*(\.[a-z][a-z0-9]*)+$`)
	pipelinePermission = regexp.MustCompile(`^pipeline:[a-z][a-z0-9]*(\.[a-z][a-z0-9]*)+$`)
	replacePermission  = regexp.MustCompile(`^ui:replace:/([a-z0-9][a-z0-9/_-]*)?$`)
)

// ValidatePermission reports whether p is a permission the panel knows.
func ValidatePermission(p string) error {
	switch {
	case p == permNetwork, p == permUIMenu:
		return nil
	case apiPermission.MatchString(p):
		return nil
	case replacePermission.MatchString(p):
		if !IsReplaceableRoute(strings.TrimPrefix(p, "ui:replace:")) {
			return fmt.Errorf("permission %q names a page plugins cannot replace", p)
		}
		return nil
	case eventPermission.MatchString(p):
		if !IsKnownEvent(strings.TrimPrefix(p, "events:")) {
			return fmt.Errorf("unknown event in permission %q", p)
		}
		return nil
	case seamPermission.MatchString(p):
		if group, ok := strings.CutPrefix(p, "seam:api:"); ok && slices.Contains(seamProtectedGroups, group) {
			return fmt.Errorf("permission %q names routes plugins cannot take part in", p)
		}
		return nil
	case pipelinePermission.MatchString(p):
		if !IsKnownPipeline(strings.TrimPrefix(p, "pipeline:")) {
			return fmt.Errorf("unknown pipeline in permission %q", p)
		}
		return nil
	}
	return fmt.Errorf("invalid permission %q", p)
}

// IsDangerousPermission marks permissions the operator must confirm explicitly.
func IsDangerousPermission(p string) bool {
	return p == permNetwork ||
		strings.HasSuffix(p, ":write") ||
		strings.HasPrefix(p, "api:settings:") ||
		strings.HasPrefix(p, "api:nodes:") ||
		strings.HasPrefix(p, "pipeline:") ||
		strings.HasPrefix(p, "seam:") ||
		strings.HasPrefix(p, "ui:replace:")
}

// SamePermissionSet compares two permission lists ignoring order and duplicates.
func SamePermissionSet(a, b []string) bool {
	return slices.Equal(normalizePermissions(a), normalizePermissions(b))
}

func normalizePermissions(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return slices.Compact(out)
}

func dangerousPermissions(perms []string) []string {
	var out []string
	for _, p := range normalizePermissions(perms) {
		if IsDangerousPermission(p) {
			out = append(out, p)
		}
	}
	return out
}
