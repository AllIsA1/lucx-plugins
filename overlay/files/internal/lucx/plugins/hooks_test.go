// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"
)

const (
	installHookPath   = "/_lucx/install"
	uninstallHookPath = "/_lucx/uninstall"
)

// hookEnv: one running plugin that declares both hooks, served by handler.
func hookEnv(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, body []byte), opts ...func(*SupervisorOptions)) (*supEnv, *pluginSocket) {
	t.Helper()
	sockDir, err := os.MkdirTemp("", "lucxh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	e := newSupEnv(t, func(m map[string]any) {
		m["hooks"] = map[string]string{"install": installHookPath, "uninstall": uninstallHookPath}
	}, opts...)
	t.Cleanup(e.sup.Close)
	e.runner.socketDir = sockDir
	e.enable(t)
	return e, listenPlugin(t, sockDir, "traffic-alerts", handler)
}

func status(code int) func(http.ResponseWriter, *http.Request, []byte) {
	return func(w http.ResponseWriter, _ *http.Request, _ []byte) { w.WriteHeader(code) }
}

func TestInstallHookRunsOnceAfterTheFirstHealthyStart(t *testing.T) {
	e, ps := hookEnv(t, status(http.StatusNoContent))
	e.reconcile()
	e.reconcile()
	reqs := ps.requests(installHookPath)
	if len(reqs) != 1 || reqs[0].Method != http.MethodPost || reqs[0].Header.Get("X-LucX-Hook") != "install" {
		t.Fatalf("install hook requests = %+v, want exactly one POST marked X-LucX-Hook: install", reqs)
	}
	if !e.entry().InstallHookDone {
		t.Error("a successful install hook must be remembered")
	}
	if err := e.sup.StopPlugin(context.Background(), "traffic-alerts"); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	if n := len(ps.requests(installHookPath)); n != 1 {
		t.Errorf("a restart called the install hook again (%d calls)", n)
	}
}

func TestInstallHookWaitsForAHealthyPlugin(t *testing.T) {
	healthy := false
	e, ps := hookEnv(t, status(http.StatusOK), func(o *SupervisorOptions) {
		o.Health = func(context.Context, string) error {
			if !healthy {
				return os.ErrDeadlineExceeded
			}
			return nil
		}
	})
	e.reconcile()
	if n := len(ps.requests(installHookPath)); n != 0 {
		t.Fatalf("hook called %d times on a plugin that is not healthy yet", n)
	}
	healthy = true
	e.reconcile()
	if n := len(ps.requests(installHookPath)); n != 1 {
		t.Errorf("hook calls after the plugin became healthy = %d, want 1", n)
	}
}

func TestFailingInstallHookIsRetriedAFewTimesAndNeverDisablesThePlugin(t *testing.T) {
	e, ps := hookEnv(t, status(http.StatusInternalServerError))
	for range 8 {
		e.reconcile()
	}
	if n := len(ps.requests(installHookPath)); n != maxHookTries {
		t.Errorf("failing hook called %d times, want %d then give up", n, maxHookTries)
	}
	if got := e.entry(); got.InstallHookDone || !got.Enabled {
		t.Errorf("entry = %+v: a failing hook must neither count as done nor disable the plugin", got)
	}
	if rt, _ := e.sup.Runtime("traffic-alerts"); rt.State != StateRunning || !rt.Healthy {
		t.Errorf("runtime = %+v", rt)
	}
}

func TestPluginsWithoutHooksAreNotCalled(t *testing.T) {
	e, ps := eventEnv(t, []string{"api:clients:read"}, status(http.StatusOK))
	e.reconcile()
	if n := len(ps.requests(installHookPath)); n != 0 {
		t.Errorf("a plugin without hooks got %d install calls", n)
	}
	if e.entry().InstallHookDone {
		t.Error("nothing was run, nothing is done")
	}
}

func TestUninstallCallsTheHookBeforeTheProcessIsStopped(t *testing.T) {
	var runningDuringHook bool
	var e *supEnv
	e, ps := hookEnv(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		if r.URL.Path == uninstallHookPath {
			runningDuringHook = e.runner.running("traffic-alerts")
		}
		w.WriteHeader(http.StatusOK)
	})
	e.reconcile()
	if err := e.m.Uninstall("traffic-alerts", false); err != nil {
		t.Fatal(err)
	}
	reqs := ps.requests(uninstallHookPath)
	if len(reqs) != 1 || reqs[0].Header.Get("X-LucX-Hook") != "uninstall" {
		t.Fatalf("uninstall hook requests = %+v", reqs)
	}
	if !runningDuringHook {
		t.Error("the plugin was already stopped when its uninstall hook ran")
	}
	if len(e.m.List()) != 0 || e.runner.running("traffic-alerts") {
		t.Error("uninstall did not finish")
	}
}

func TestUninstallGoesAheadWhenTheHookFailsOrHangs(t *testing.T) {
	cases := map[string]func(w http.ResponseWriter, r *http.Request, body []byte){
		"fails": func(w http.ResponseWriter, _ *http.Request, _ []byte) { w.WriteHeader(http.StatusInternalServerError) },
		"hangs": func(_ http.ResponseWriter, _ *http.Request, _ []byte) { time.Sleep(2 * time.Second) },
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			e, _ := hookEnv(t, handler, func(o *SupervisorOptions) { o.HookTimeout = 100 * time.Millisecond })
			e.reconcile()
			started := time.Now()
			if err := e.m.Uninstall("traffic-alerts", false); err != nil {
				t.Fatalf("a hook that %s must not block uninstall: %v", name, err)
			}
			if time.Since(started) > time.Second {
				t.Errorf("uninstall took %v", time.Since(started))
			}
			if len(e.m.List()) != 0 {
				t.Error("plugin still registered")
			}
		})
	}
}

func TestUninstallSkipsTheHookWhenThePluginIsNotRunning(t *testing.T) {
	e, ps := hookEnv(t, status(http.StatusOK))
	if err := e.m.SetEnabled("traffic-alerts", false); err != nil {
		t.Fatal(err)
	}
	e.reconcile()
	if err := e.m.Uninstall("traffic-alerts", false); err != nil {
		t.Fatal(err)
	}
	if n := len(ps.requests(uninstallHookPath)); n != 0 {
		t.Errorf("the hook was called %d times on a plugin that was not running", n)
	}
}
