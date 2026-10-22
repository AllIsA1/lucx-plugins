// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"encoding/json"
	"testing"
	"time"
)

func uiEntry(id string, mutate func(e *Entry)) Entry {
	e := Entry{
		ID: id, Name: "Plugin " + id, Type: TypeUI, Scope: ScopeMaster, Enabled: true,
		InstalledAt: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC),
		UI: UISpec{
			Menu:     []MenuItem{{Title: "Open " + id, Icon: "bell", Path: "/ui/index.html"}},
			Replaces: []string{"/clients"},
		},
	}
	if mutate != nil {
		mutate(&e)
	}
	return e
}

func TestUIStateShowsOnlyWhatRunsOnThisPanel(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(e *Entry)
		shown  bool
	}{
		{"enabled local master plugin", nil, true},
		{"disabled plugin", func(e *Entry) { e.Enabled = false }, false},
		{"local plugin for nodes only", func(e *Entry) { e.Scope = ScopeNode }, false},
		{"local plugin for both", func(e *Entry) { e.Scope = ScopeBoth }, true},
		{"master-pushed node plugin", func(e *Entry) { e.ManagedByMaster, e.Scope = true, ScopeNode }, true},
		{"master-pushed master-only plugin", func(e *Entry) { e.ManagedByMaster = true }, false},
		{"process plugin", func(e *Entry) { e.Type = TypeProcess }, true},
		{"hook plugin", func(e *Entry) { e.Type = TypeHook }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := BuildUIState([]Entry{uiEntry("page-a", tc.mutate)})
			if got := len(state.Menu) == 1 && len(state.Replaces) == 1; got != tc.shown {
				t.Errorf("shown = %v, want %v (state %+v)", got, tc.shown, state)
			}
		})
	}
}

func TestUIStateDescribesTheItems(t *testing.T) {
	state := BuildUIState([]Entry{uiEntry("page-a", func(e *Entry) {
		e.UI.Replaces = []string{"/clients", "/"}
		e.UI.Menu = append(e.UI.Menu, MenuItem{Title: "Second", Path: "/ui/two.html"})
	})})
	if len(state.Menu) != 2 || state.Menu[0] != (UIMenuItem{PluginID: "page-a", PluginName: "Plugin page-a", Title: "Open page-a", Icon: "bell", Path: "/ui/index.html"}) || state.Menu[1].Title != "Second" {
		t.Errorf("menu = %+v", state.Menu)
	}
	// A replacement page lives at ui/replace/<route>/ so the manifest needs no extra field.
	if got := state.Replaces["/clients"]; got != (UIReplace{PluginID: "page-a", PluginName: "Plugin page-a", Path: "/ui/replace/clients/"}) {
		t.Errorf("replace /clients = %+v", got)
	}
	if got := state.Replaces["/"].Path; got != "/ui/replace/" {
		t.Errorf("replace / path = %q", got)
	}
}

func TestUIStateIsStableAndFirstReplacementWins(t *testing.T) {
	early := uiEntry("zzz-early", nil)
	early.InstalledAt = early.InstalledAt.Add(-time.Hour)
	late := uiEntry("aaa-late", nil)
	state := BuildUIState([]Entry{late, early})
	if state.Replaces["/clients"].PluginID != "zzz-early" {
		t.Errorf("replacement = %+v, want the earlier install to win", state.Replaces["/clients"])
	}
	if len(state.Menu) != 2 || state.Menu[0].PluginID != "zzz-early" {
		t.Errorf("menu order = %+v, want install order", state.Menu)
	}
}

func TestUIStateIgnoresPagesThatAreNotReplaceable(t *testing.T) {
	state := BuildUIState([]Entry{uiEntry("page-a", func(e *Entry) { e.UI.Replaces = []string{"/settings"} })})
	if len(state.Replaces) != 0 {
		t.Errorf("a hand-edited registry replaced a protected page: %+v", state.Replaces)
	}
}

func TestEmptyUIStateEncodesAsEmptyCollections(t *testing.T) {
	raw, err := json.Marshal(BuildUIState(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"menu":[],"replaces":{}}` {
		t.Errorf("empty state = %s, want collections the UI can iterate", raw)
	}
}
