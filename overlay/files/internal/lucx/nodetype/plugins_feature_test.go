// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package nodetype

import (
	"slices"
	"testing"
)

// A node that announces the plugin feature has it; a LucX node whose features
// are merely inferred from its version (it predates plugins) must not be
// assumed to, or a master would send it commands it cannot understand.
func TestOnlyANodeThatSaysSoSupportsPlugins(t *testing.T) {
	defer func(prev func() bool) { PluginSystemAvailable = prev }(PluginSystemAvailable)
	PluginSystemAvailable = func() bool { return true }
	if !slices.Contains(LocalHello().Features, FeaturePlugins) {
		t.Error("a panel whose plugin system answers must announce it in /lucx/hello")
	}
	PluginSystemAvailable = func() bool { return false }
	if slices.Contains(LocalHello().Features, FeaturePlugins) {
		t.Error("a panel whose plugin system does not answer must not announce plugins: a master would send commands nobody can run")
	}
	PluginSystemAvailable = nil
	if slices.Contains(LocalHello().Features, FeaturePlugins) {
		t.Error("a panel with no plugin link at all must not announce plugins")
	}
	if slices.Contains(DefaultLucXFeatures, FeaturePlugins) {
		t.Error("DefaultLucXFeatures is the fallback for older nodes and must not claim plugins")
	}
	inferred := FromPanelVersion("3.8.0-lucx.250")
	if inferred.HasFeature(FeaturePlugins) {
		t.Errorf("a version-inferred node claims plugins: %+v", inferred)
	}
	announced := &NodeInfo{NodeType: TypeLucX, Features: append(slices.Clone(DefaultLucXFeatures), FeaturePlugins)}
	if !announced.HasFeature(FeaturePlugins) {
		t.Error("an announced feature must be recognised")
	}
}
