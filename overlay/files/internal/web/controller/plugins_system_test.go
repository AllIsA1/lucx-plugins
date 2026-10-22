// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package controller

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/lucx/plugins"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

type systemSpy struct {
	ctrl    *plugins.SystemControl
	started []string
}

func newSystemSpy(t *testing.T, managed bool) *systemSpy {
	t.Helper()
	dir := t.TempDir()
	state, units := filepath.Join(dir, "state"), filepath.Join(dir, "units")
	for _, p := range []string{state, units} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	spy := &systemSpy{}
	spy.ctrl = &plugins.SystemControl{
		ConfigPath: filepath.Join(dir, "config"),
		UnitDir:    units,
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			if len(args) >= 3 && args[0] == "start" {
				spy.started = append(spy.started, args[2])
			}
			return nil, nil
		},
	}
	if managed {
		if err := os.WriteFile(spy.ctrl.ConfigPath, []byte("STATE_DIR="+state+"\nRESTART_AT=04:00\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(units, "lucx-plugins-apply.service"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		doc := `{"upstream":"v3.10.2-lucx.307","release":"v3.10.2-lucx.307-plugins.3","pending":true}`
		if err := os.WriteFile(filepath.Join(state, "state.json"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return spy
}

func systemHarness(t *testing.T, scope string, spy *systemSpy) *pluginsHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(sessions.Sessions("3x-ui", cookie.NewStore([]byte("plugins-test-secret"))))
	api := engine.Group("/panel/api")
	api.Use(func(c *gin.Context) {
		if scope != "" {
			c.Set("api_authed", true)
			c.Set("api_token_scope", scope)
		}
		c.Next()
	})
	NewPluginsController(api.Group("/plugins"), &service.PluginService{System: spy.ctrl})
	return &pluginsHarness{engine: engine}
}

func TestSystemStatusRouteReportsTheInstalledBuild(t *testing.T) {
	h := systemHarness(t, "", newSystemSpy(t, true))
	rec := rawCall(h, http.MethodGet, "/panel/api/plugins/system", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	for _, want := range []string{`"managed":true`, `"release":"v3.10.2-lucx.307-plugins.3"`, `"pending":true`, `"restartAt":"04:00"`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("response lacks %s: %s", want, rec.Body.String())
		}
	}
}

func TestSystemCheckAndUpdateRoutesStartTheUnitsAndAnswerWithoutAToast(t *testing.T) {
	spy := newSystemSpy(t, true)
	h := systemHarness(t, "", spy)
	for _, path := range []string{"/panel/api/plugins/system/check", "/panel/api/plugins/system/update"} {
		if r := h.call(t, http.MethodPost, path, nil); !r.Success || r.Msg != "" {
			t.Errorf("%s: %+v, a success carries an empty msg", path, r)
		}
	}
	if len(spy.started) != 2 || spy.started[0] != "lucx-plugins-sync.service" || spy.started[1] != "lucx-plugins-apply.service" {
		t.Errorf("started = %v", spy.started)
	}
}

func TestSystemRoutesSayWhenThePluginSystemIsNotManaged(t *testing.T) {
	spy := newSystemSpy(t, false)
	h := systemHarness(t, "", spy)
	if r := h.call(t, http.MethodPost, "/panel/api/plugins/system/update", nil); r.Success || !strings.Contains(r.Msg, "not managed") {
		t.Errorf("update on an unmanaged host: %+v", r)
	}
	if rec := rawCall(h, http.MethodGet, "/panel/api/plugins/system", ""); !strings.Contains(rec.Body.String(), `"managed":false`) || !strings.Contains(rec.Body.String(), `"reason":"not-installed"`) {
		t.Errorf("status: %s", rec.Body.String())
	}
	if len(spy.started) != 0 {
		t.Errorf("nothing may run on an unmanaged host: %v", spy.started)
	}
}

func TestAMasterPanelCannotTouchThePluginSystemOfANode(t *testing.T) {
	spy := newSystemSpy(t, true)
	h := systemHarness(t, model.ApiScopeNodeSync, spy)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/panel/api/plugins/system"},
		{http.MethodPost, "/panel/api/plugins/system/check"},
		{http.MethodPost, "/panel/api/plugins/system/update"},
	} {
		if rec := rawCall(h, c.method, c.path, ""); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s = %d, a node-sync token must be refused", c.method, c.path, rec.Code)
		}
	}
	if len(spy.started) != 0 {
		t.Errorf("a master started %v on a node", spy.started)
	}
}
