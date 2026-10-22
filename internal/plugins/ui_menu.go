// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import (
	"slices"
	"sort"
	"strings"
)

// UIMenuItem is a sidebar entry a plugin adds.
type UIMenuItem struct {
	PluginID   string `json:"pluginId"`
	PluginName string `json:"pluginName"`
	Title      string `json:"title"`
	Icon       string `json:"icon"`
	Path       string `json:"path"`
}

// UIReplace points a built-in page at the plugin page that stands in for it.
type UIReplace struct {
	PluginID   string `json:"pluginId"`
	PluginName string `json:"pluginName"`
	Path       string `json:"path"`
}

// UIState is everything plugins add to the interface of this panel right now.
type UIState struct {
	Menu     []UIMenuItem         `json:"menu"`
	Replaces map[string]UIReplace `json:"replaces"`
}

// BuildUIState lists what enabled plugins that run on this panel add, in install order.
// A page two plugins claim goes to the earlier install.
func BuildUIState(entries []Entry) UIState {
	state := UIState{Menu: []UIMenuItem{}, Replaces: map[string]UIReplace{}}
	sorted := slices.Clone(entries)
	sort.SliceStable(sorted, func(i, j int) bool {
		if !sorted[i].InstalledAt.Equal(sorted[j].InstalledAt) {
			return sorted[i].InstalledAt.Before(sorted[j].InstalledAt)
		}
		return sorted[i].ID < sorted[j].ID
	})
	for _, e := range sorted {
		if !showsInterface(e) {
			continue
		}
		for _, item := range e.UI.Menu {
			state.Menu = append(state.Menu, UIMenuItem{PluginID: e.ID, PluginName: e.Name, Title: item.Title, Icon: item.Icon, Path: item.Path})
		}
		for _, route := range e.UI.Replaces {
			if _, taken := state.Replaces[route]; taken || !IsReplaceableRoute(route) {
				continue
			}
			state.Replaces[route] = UIReplace{PluginID: e.ID, PluginName: e.Name, Path: replacementPath(route)}
		}
	}
	return state
}

// replacementPath is where a plugin keeps the page that stands in for route.
func replacementPath(route string) string {
	return "/ui/replace" + strings.TrimSuffix(route, "/") + "/"
}

func showsInterface(e Entry) bool {
	return e.Enabled && e.Type != TypeHook && runsOnThisPanel(e)
}
