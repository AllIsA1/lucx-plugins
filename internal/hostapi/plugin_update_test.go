// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AllIsA1/lucx-plugins/internal/plugins"
)

const (
	updateCommit2 = "2222222222222222222222222222222222222222"
	updateV1      = `{"id":"traffic-alerts","name":"Traffic Alerts","version":"1.2.0","apiVersion":"1","type":"process",` +
		`"run":{"command":"bin/${arch}/alerts"},"permissions":["api:clients:read"]}`
	updateV2Same = `{"id":"traffic-alerts","name":"Traffic Alerts","version":"1.3.0","apiVersion":"1","type":"process",` +
		`"run":{"command":"bin/${arch}/alerts"},"permissions":["api:clients:read"]}`
	updateV2Wider = `{"id":"traffic-alerts","name":"Traffic Alerts","version":"1.3.0","apiVersion":"1","type":"process",` +
		`"run":{"command":"bin/${arch}/alerts"},"permissions":["api:clients:read","network"]}`
)

type updateFixture struct {
	h      *pluginsHarness
	m      *plugins.Manager
	broken *atomic.Bool
	mu     sync.Mutex
	served []byte
}

func (f *updateFixture) publish(t *testing.T, manifest, commit string) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.served = pluginsArchiveAt(t, manifest, commit)
}

// newUpdateFixture installs and starts 1.2.0 under a supervisor that really reconciles.
func newUpdateFixture(t *testing.T) *updateFixture {
	t.Helper()
	f := &updateFixture{broken: &atomic.Bool{}}
	f.served = pluginsArchiveAt(t, updateV1, pluginsTestCommit)
	f.m = newManagerServingFunc(t, func() []byte {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.served
	})
	sup := plugins.NewSupervisor(f.m, newStubRunner(), plugins.SupervisorOptions{
		GOARCH:   "amd64",
		Interval: 30 * time.Millisecond,
		Health: func(context.Context, string) error {
			if f.broken.Load() {
				return context.DeadlineExceeded
			}
			return nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	go sup.Run(ctx)
	t.Cleanup(cancel)
	svc := &PluginService{
		Provider:            func() (*plugins.Manager, error) { return f.m, nil },
		Supervisor:          func() *plugins.Supervisor { return sup },
		UpdateHealthTimeout: 1500 * time.Millisecond,
	}
	f.h = newPluginsHarnessWith(t, svc)
	p := previewOK(t, f.h)
	if r := f.h.call(t, http.MethodPost, "/plugins/install", map[string]any{"token": p.Token, "permissions": p.Manifest.Permissions}); !r.Success {
		t.Fatalf("install: %s", r.Msg)
	}
	if r := f.h.call(t, http.MethodPost, "/plugins/enable/traffic-alerts", nil); !r.Success {
		t.Fatalf("enable: %s", r.Msg)
	}
	f.waitHealthy(t)
	return f
}

func (f *updateFixture) entry(t *testing.T) pluginView {
	t.Helper()
	for _, p := range f.h.state(t).Plugins {
		if p.ID == "traffic-alerts" {
			return p
		}
	}
	t.Fatal("plugin missing from the list")
	return pluginView{}
}

func (f *updateFixture) waitHealthy(t *testing.T) {
	t.Helper()
	waitUntil(t, "the plugin to be healthy", func() bool {
		rt := f.entry(t).Runtime
		return rt != nil && rt.State == plugins.StateRunning && rt.Healthy
	})
}

func (f *updateFixture) preview(t *testing.T) plugins.UpdatePreview {
	t.Helper()
	r := f.h.call(t, http.MethodPost, "/plugins/update-preview/traffic-alerts", map[string]string{})
	if !r.Success {
		t.Fatalf("update-preview: %s", r.Msg)
	}
	var up plugins.UpdatePreview
	if err := json.Unmarshal(r.Obj, &up); err != nil {
		t.Fatal(err)
	}
	return up
}

func (f *updateFixture) update(t *testing.T, token string, perms []string) pluginsResp {
	t.Helper()
	return f.h.call(t, http.MethodPost, "/plugins/update/traffic-alerts", map[string]any{"token": token, "permissions": perms})
}

func TestUpdateKeepsAHealthyPluginRunningOnTheNewVersion(t *testing.T) {
	f := newUpdateFixture(t)
	f.publish(t, updateV2Same, updateCommit2)

	up := f.preview(t)
	if up.UpToDate || up.NeedsConfirmation || up.Manifest.Version != "1.3.0" || up.Token == "" {
		t.Fatalf("preview = %+v", up)
	}
	if r := f.update(t, up.Token, up.Manifest.Permissions); !r.Success {
		t.Fatalf("update: %s", r.Msg)
	}
	e := f.entry(t)
	if e.Version != "1.3.0" || e.Commit != updateCommit2 || !e.Enabled || e.Previous == nil || e.Previous.Version != "1.2.0" {
		t.Fatalf("entry after update = %+v", e)
	}
	f.waitHealthy(t)
}

func TestUpdateGoesBackWhenTheNewVersionNeverBecomesHealthy(t *testing.T) {
	f := newUpdateFixture(t)
	f.publish(t, updateV2Same, updateCommit2)
	up := f.preview(t)

	f.broken.Store(true)
	r := f.update(t, up.Token, up.Manifest.Permissions)
	if r.Success || !strings.Contains(r.Msg, "previous version was restored") {
		t.Fatalf("update of a broken version = %+v, want a failure that says it went back", r)
	}
	e := f.entry(t)
	if e.Version != "1.2.0" || e.Commit != pluginsTestCommit || e.Previous != nil || !e.Enabled {
		t.Fatalf("entry after the automatic rollback = %+v", e)
	}
	f.broken.Store(false)
	f.waitHealthy(t)
}

func TestUpdateWithGrownPermissionsNeedsThemConfirmed(t *testing.T) {
	f := newUpdateFixture(t)
	f.publish(t, updateV2Wider, updateCommit2)
	up := f.preview(t)
	if !up.NeedsConfirmation || len(up.Diff.DangerousAdded) != 1 || up.Diff.DangerousAdded[0] != "network" {
		t.Fatalf("preview = %+v", up)
	}
	if r := f.update(t, up.Token, []string{"api:clients:read"}); r.Success {
		t.Fatal("an update that adds network went through on the old permissions")
	}
	if e := f.entry(t); e.Version != "1.2.0" {
		t.Errorf("entry changed: %+v", e)
	}
	up = f.preview(t)
	if r := f.update(t, up.Token, []string{"api:clients:read", "network"}); !r.Success {
		t.Fatalf("confirmed update: %s", r.Msg)
	}
}

func TestRollbackRouteGoesBackToThePreviousVersion(t *testing.T) {
	f := newUpdateFixture(t)
	if r := f.h.call(t, http.MethodPost, "/plugins/rollback/traffic-alerts", nil); r.Success {
		t.Fatal("rollback with nothing to go back to succeeded")
	}
	f.publish(t, updateV2Same, updateCommit2)
	up := f.preview(t)
	if r := f.update(t, up.Token, up.Manifest.Permissions); !r.Success {
		t.Fatal(r.Msg)
	}
	if r := f.h.call(t, http.MethodPost, "/plugins/rollback/traffic-alerts", nil); !r.Success {
		t.Fatalf("rollback: %s", r.Msg)
	}
	if e := f.entry(t); e.Version != "1.2.0" || e.Previous != nil {
		t.Fatalf("entry = %+v", e)
	}
	f.waitHealthy(t)
}

func TestUpdatePreviewOfTheCurrentVersionSaysUpToDate(t *testing.T) {
	f := newUpdateFixture(t)
	if up := f.preview(t); !up.UpToDate || up.Token != "" {
		t.Errorf("preview = %+v", up)
	}
}
