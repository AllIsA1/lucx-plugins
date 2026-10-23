// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
)

// A seam is a place where a plugin takes part in what the panel does with an HTTP request: it may
// look at and change a request before the panel handles it (before), take the request over (replace)
// or change what the panel answered (after). The panel asks the plugin system at the moment of the
// call, so switching a plugin on or off needs no restart; whenever the plugin does not answer in
// time or answers wrongly, the panel's own code runs as if the plugin were not there.
const (
	SeamEngineAPI = "api" // the panel's own API, routes under /panel/api
	SeamEngineSub = "sub" // the subscription server

	SeamBefore  = "before"
	SeamReplace = "replace"
	SeamAfter   = "after"

	maxSeams = 64
)

// Seam is one entry of a manifest's seams: where the plugin wants to take part and how.
type Seam struct {
	Engine  string   `json:"engine"`
	Mode    string   `json:"mode"`
	Methods []string `json:"methods,omitempty"` // empty: every method
	// Route is a gin route pattern relative to the engine (for the API, to /panel/api): an exact
	// pattern, "<group>/*" for everything below a first path segment, or "*" for every route.
	Route string `json:"route"`
}

var (
	seamMethods      = []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"}
	seamRoutePattern = regexp.MustCompile(`^(\*|/|/[A-Za-z0-9:_.-]+(/[A-Za-z0-9:_.-]+)*(/\*)?)$`)
	seamGroupPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	seamPermission   = regexp.MustCompile(`^seam:(sub|api:([a-z][a-z0-9-]*|\*))$`)

	// seamProtectedGroups are API route groups no plugin may take part in, whatever it is granted.
	seamProtectedGroups = []string{"setting", "plugins", "lucx"}
	// seamProtectedRoutes are single routes (or "<prefix>/*" families) inside otherwise open groups.
	// Logging in and out, the CSRF token and two-factor checks are not under /panel/api at all.
	seamProtectedRoutes = []string{
		"/server/importDB", "/server/getDb", "/server/updatePanel", "/server/getMigration",
		"/nodes/mtls/*", "/nodes/updatePanel",
		"/backuptotgbot",
	}
)

// seamGroup is the first path segment of a route, the unit the permission is granted in.
func seamGroup(route string) string {
	trimmed := strings.TrimPrefix(route, "/")
	group, _, _ := strings.Cut(trimmed, "/")
	return group
}

// IsProtectedSeamRoute reports whether the route pattern is one plugins can never intercept: the
// settings and API tokens, the database and the panel's own update, the node CA, and the plugin
// system's own routes.
func IsProtectedSeamRoute(engine, route string) bool {
	if engine != SeamEngineAPI {
		return false
	}
	if slices.Contains(seamProtectedGroups, seamGroup(route)) {
		return true
	}
	for _, p := range seamProtectedRoutes {
		if routeCovers(p, route) {
			return true
		}
	}
	return false
}

// routeCovers: pattern "<prefix>/*" covers every route below the prefix, any other pattern only itself.
func routeCovers(pattern, route string) bool {
	if prefix, ok := strings.CutSuffix(pattern, "*"); ok && strings.HasSuffix(prefix, "/") {
		return strings.HasPrefix(route, prefix) && len(route) > len(prefix)
	}
	return pattern == route
}

// SeamRouteMatches reports whether a seam declared on seamRoute takes part in the route pattern the
// panel resolved. A protected route is never matched, not even by "*".
func SeamRouteMatches(engine, seamRoute, route string) bool {
	if IsProtectedSeamRoute(engine, route) {
		return false
	}
	if seamRoute == "*" {
		return true
	}
	return routeCovers(seamRoute, route)
}

// SeamMethodMatches reports whether the seam takes part in the HTTP method (HEAD counts as GET).
func (s Seam) SeamMethodMatches(method string) bool {
	if len(s.Methods) == 0 {
		return true
	}
	if method == "HEAD" && !slices.Contains(s.Methods, "HEAD") {
		method = "GET"
	}
	return slices.Contains(s.Methods, method)
}

// RequiredPermission is the permission a plugin must hold to declare the seam.
func (s Seam) RequiredPermission() string {
	if s.Engine == SeamEngineSub {
		return "seam:sub"
	}
	if s.Route == "*" {
		return "seam:api:*"
	}
	return "seam:api:" + seamGroup(s.Route)
}

func (s Seam) key() string {
	methods := slices.Clone(s.Methods)
	slices.Sort(methods)
	return s.Engine + "|" + s.Mode + "|" + s.Route + "|" + strings.Join(methods, ",")
}

func (m *Manifest) validateSeams() error {
	if len(m.Seams) == 0 {
		return nil
	}
	if m.Type != TypeProcess {
		return fmt.Errorf("seams need a running plugin: type %q cannot declare them", m.Type)
	}
	if len(m.Seams) > maxSeams {
		return fmt.Errorf("seams has more than %d entries", maxSeams)
	}
	held := normalizePermissions(m.Permissions)
	seen := map[string]bool{}
	for _, s := range m.Seams {
		if err := s.validate(); err != nil {
			return err
		}
		if want := s.RequiredPermission(); !slices.Contains(held, want) {
			return fmt.Errorf("seam %s %s requires the permission %q", s.Engine, s.Route, want)
		}
		if seen[s.key()] {
			return fmt.Errorf("seam %s %s %s is declared twice", s.Engine, s.Mode, s.Route)
		}
		seen[s.key()] = true
	}
	return nil
}

func (s Seam) validate() error {
	switch s.Engine {
	case SeamEngineAPI, SeamEngineSub:
	default:
		return fmt.Errorf("invalid seam engine %q: use %s or %s", s.Engine, SeamEngineAPI, SeamEngineSub)
	}
	switch s.Mode {
	case SeamBefore, SeamAfter, SeamReplace:
	default:
		return fmt.Errorf("invalid seam mode %q: use before, replace or after", s.Mode)
	}
	for _, method := range s.Methods {
		if !slices.Contains(seamMethods, method) {
			return fmt.Errorf("invalid seam method %q: use one of %s", method, strings.Join(seamMethods, ", "))
		}
	}
	if len(s.Route) > maxPathIn || !seamRoutePattern.MatchString(s.Route) {
		return fmt.Errorf("invalid seam route %q: an exact route like /clients/list, <group>/* or *", s.Route)
	}
	clean := strings.TrimSuffix(s.Route, "/*")
	if clean != "*" && clean != "" && path.Clean(clean) != clean {
		return fmt.Errorf("invalid seam route %q: it must be a clean path", s.Route)
	}
	if s.Mode == SeamReplace && s.Route == "*" {
		return fmt.Errorf("seam mode replace cannot take every route; name a route or a group")
	}
	if s.Engine == SeamEngineAPI && s.Route != "*" {
		if !seamGroupPattern.MatchString(seamGroup(s.Route)) {
			return fmt.Errorf("invalid seam route %q: the first path segment names the group", s.Route)
		}
		if s.Route == "/" {
			return fmt.Errorf("invalid seam route %q", s.Route)
		}
		if IsProtectedSeamRoute(s.Engine, clean) || IsProtectedSeamRoute(s.Engine, s.Route) {
			return fmt.Errorf("seam route %q is protected: plugins cannot take part in it", s.Route)
		}
	}
	return nil
}
