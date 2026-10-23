// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/AllIsA1/lucx-plugins/internal/hostupdate"
	"github.com/AllIsA1/lucx-plugins/internal/plugins"
)

type fakeUpdater struct {
	mu       sync.Mutex
	status   hostupdate.Status
	checks   int
	applies  int
	checkErr error
	applyErr error
}

func (f *fakeUpdater) Status() hostupdate.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

func (f *fakeUpdater) Check(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks++
	return f.checkErr
}

func (f *fakeUpdater) Apply(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applies++
	return f.applyErr
}

func (f *fakeUpdater) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.checks, f.applies
}

func systemHarness(t *testing.T, scope string, up SystemUpdater, restart func()) *pluginsHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	api := engine.Group("/panel/api")
	api.Use(func(c *gin.Context) {
		if scope != "" {
			c.Request.Header.Set(headerScope, scope)
		}
		c.Next()
	})
	svc := &PluginService{Provider: func() (*plugins.Manager, error) { return nil, plugins.ErrDisabled }, Updater: up, RestartHost: restart}
	NewPluginsController(api.Group("/plugins"), svc)
	return &pluginsHarness{engine: engine}
}

func TestSystemStatusIsReportedAndAnUnmanagedHostSaysSo(t *testing.T) {
	up := &fakeUpdater{status: hostupdate.Status{Managed: true, Version: "host-3", Available: "host-4"}}
	r := systemHarness(t, "", up, nil).callAt(t, http.MethodGet, "/plugins/system", nil)
	var st hostupdate.Status
	if err := json.Unmarshal(r.Obj, &st); err != nil || !r.Success || st.Version != "host-3" || st.Available != "host-4" || !st.Managed {
		t.Errorf("status = %+v (%v) %+v", st, err, r)
	}

	r = systemHarness(t, "", nil, nil).callAt(t, http.MethodGet, "/plugins/system", nil)
	var none hostupdate.Status
	if err := json.Unmarshal(r.Obj, &none); err != nil || !r.Success || none.Managed {
		t.Errorf("without an updater: %+v (%v), want success and managed=false", none, err)
	}
}

func TestSystemCheckOnlyChecks(t *testing.T) {
	up := &fakeUpdater{}
	restarted := make(chan struct{}, 1)
	h := systemHarness(t, "", up, func() { restarted <- struct{}{} })
	if r := h.callAt(t, http.MethodPost, "/plugins/system/check", nil); !r.Success {
		t.Fatalf("check: %+v", r)
	}
	if c, a := up.counts(); c != 1 || a != 0 {
		t.Errorf("checks=%d applies=%d, want 1 and 0", c, a)
	}
	up.checkErr = errors.New("no network")
	if r := h.callAt(t, http.MethodPost, "/plugins/system/check", nil); r.Success || r.Msg == "" {
		t.Errorf("a failed check = %+v, want the failure and its reason", r)
	}
	select {
	case <-restarted:
		t.Error("a check restarted the host")
	case <-time.After(150 * time.Millisecond):
	}
}

func TestSystemUpdateRestartsTheHostOnlyAfterAnswering(t *testing.T) {
	up := &fakeUpdater{}
	restarted := make(chan struct{}, 2)
	h := systemHarness(t, "", up, func() { restarted <- struct{}{} })
	if r := h.callAt(t, http.MethodPost, "/plugins/system/update", nil); !r.Success {
		t.Fatalf("update: %+v", r)
	}
	select {
	case <-restarted:
	case <-time.After(3 * time.Second):
		t.Fatal("the host was not restarted after a successful update")
	}
	if _, a := up.counts(); a != 1 {
		t.Errorf("applies = %d, want 1", a)
	}

	up.applyErr = errors.New("the download does not match the signed hash")
	if r := h.callAt(t, http.MethodPost, "/plugins/system/update", nil); r.Success || r.Msg == "" {
		t.Errorf("a failed update = %+v", r)
	}
	select {
	case <-restarted:
		t.Error("the host restarted although the update failed")
	case <-time.After(1200 * time.Millisecond):
	}
}

func TestMasterPanelsAndPluginsCannotTouchTheSystemRoutes(t *testing.T) {
	up := &fakeUpdater{}
	restarted := make(chan struct{}, 1)
	h := systemHarness(t, scopeNodeSync, up, func() { restarted <- struct{}{} })
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/plugins/system"}, {http.MethodPost, "/plugins/system/check"}, {http.MethodPost, "/plugins/system/update"},
	} {
		if r := h.callAt(t, c.method, c.path, nil); r.Success {
			t.Errorf("%s %s answered a master panel: %+v", c.method, c.path, r)
		}
	}
	if c, a := up.counts(); c != 0 || a != 0 {
		t.Errorf("checks=%d applies=%d for a master panel", c, a)
	}
	select {
	case <-restarted:
		t.Error("a master panel restarted the host")
	case <-time.After(1200 * time.Millisecond):
	}
	for _, route := range plugins.AllowedRoutes([]string{"api:clients:read", "api:clients:write", "api:server:write", "api:xray:write"}) {
		if len(route.Pattern) >= 8 && route.Pattern[:8] == "/plugins" {
			t.Errorf("a plugin permission reaches %s %s", route.Method, route.Pattern)
		}
	}
}
