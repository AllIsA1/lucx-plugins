// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostapi

import (
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/AllIsA1/lucx-plugins/internal/plugins"
)

// The panel integration registers the host's routes from a table of its own and keeps a copy of
// this list as a golden file. If a route is added here and not there (or the other way round), the
// panel answers 404 for something the host serves, or documents something that does not exist.
func TestRoutesAreExactlyThosePanelIntegrationForwards(t *testing.T) {
	golden, err := os.ReadFile("../../overlay/files/internal/lucx/hostlink/testdata/routes.golden")
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(&PluginService{Provider: func() (*plugins.Manager, error) { return nil, plugins.ErrDisabled }})
	var got []string
	for _, r := range srv.engine.Routes() {
		if strings.HasPrefix(r.Path, "/panel/") {
			got = append(got, r.Method+" "+r.Path)
		}
	}
	sort.Strings(got)
	if strings.Join(got, "\n")+"\n" != string(golden) {
		t.Errorf("the host serves:\n%s\n\nthe panel integration forwards (golden):\n%s", strings.Join(got, "\n"), golden)
	}
}
