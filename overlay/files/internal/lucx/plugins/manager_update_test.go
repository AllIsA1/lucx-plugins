// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	commitV2 = "2222222222222222222222222222222222222222"
	commitV3 = "3333333333333333333333333333333333333333"
	updURL   = "https://github.com/acme/alerts"
	updPath  = "/acme/alerts/tar.gz/HEAD"
)

// archiveAt is the traffic-alerts plugin at a commit, with the binary content naming its version.
func archiveAt(t *testing.T, commit, version string, mutate func(m map[string]any)) []byte {
	t.Helper()
	return buildArchive(t, commit,
		dir("alerts-"+commit[:7]+"/"),
		file("alerts-"+commit[:7]+"/lucx-plugin.json", string(manifestJSON(t, func(m map[string]any) {
			m["version"] = version
			if mutate != nil {
				mutate(m)
			}
		}))),
		file("alerts-"+commit[:7]+"/bin/amd64/alerts", "binary "+version),
	)
}

type fakeProc struct {
	stopped []string
	err     error
}

func (f *fakeProc) StopPlugin(_ context.Context, id string) error {
	f.stopped = append(f.stopped, id)
	return f.err
}

// installedV1 installs 1.2.0 (default permissions), enabled, with data, and returns the env.
func installedV1(t *testing.T) (*testEnv, *fakeProc) {
	t.Helper()
	env := newEnv(t, map[string][]byte{updPath: archiveAt(t, testCommit, "1.2.0", nil)})
	proc := &fakeProc{}
	env.m.SetProcessController(proc)
	p, err := env.m.Preview(context.Background(), updURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.m.Install(p.Token, defaultPerms()); err != nil {
		t.Fatal(err)
	}
	if err := env.m.SetEnabled("traffic-alerts", true); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(env.dataDir, "traffic-alerts")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "state.db"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	return env, proc
}

func (e *testEnv) publish(archive []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.archives[updPath] = archive
}

func binaryOf(t *testing.T, env *testEnv) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(env.codeDir, "traffic-alerts", "bin", "amd64", "alerts"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestPreviewUpdateSaysWhatChanges(t *testing.T) {
	env, _ := installedV1(t)
	ctx := context.Background()

	env.publish(archiveAt(t, commitV2, "1.3.0", func(m map[string]any) {
		m["permissions"] = []string{"api:clients:read", "network"}
	}))
	up, err := env.m.PreviewUpdate(ctx, "traffic-alerts", "")
	if err != nil {
		t.Fatal(err)
	}
	if up.UpToDate || up.Token == "" || up.Commit != commitV2 || up.Manifest.Version != "1.3.0" {
		t.Fatalf("preview = %+v", up)
	}
	if up.Current.Version != "1.2.0" || up.Current.Commit != testCommit {
		t.Errorf("current = %+v", up.Current)
	}
	d := up.Diff
	if !slices.Equal(d.Added, []string{"network"}) || !slices.Equal(d.Removed, []string{"events:client.traffic.exhausted"}) ||
		!slices.Equal(d.Kept, []string{"api:clients:read"}) || !slices.Equal(d.DangerousAdded, []string{"network"}) {
		t.Errorf("diff = %+v", d)
	}
	if !up.NeedsConfirmation {
		t.Error("a new dangerous permission must need confirmation")
	}
}

func TestPreviewUpdateOnlyAsksForConfirmationWhenTrustGrows(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		mutate  func(m map[string]any)
		confirm bool
	}{
		{"same permissions", nil, false},
		{"fewer permissions", func(m map[string]any) { m["permissions"] = []string{"api:clients:read"} }, false},
		{"a new harmless permission", func(m map[string]any) {
			m["permissions"] = []string{"api:clients:read", "events:client.traffic.exhausted", "events:client.created"}
		}, true},
		{"a new dangerous permission", func(m map[string]any) {
			m["permissions"] = []string{"api:clients:read", "events:client.traffic.exhausted", "api:clients:write"}
		}, true},
		{"another scope", func(m map[string]any) { m["scope"] = "both" }, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env, _ := installedV1(t)
			env.publish(archiveAt(t, commitV2, "1.3.0", tc.mutate))
			up, err := env.m.PreviewUpdate(ctx, "traffic-alerts", "")
			if err != nil {
				t.Fatal(err)
			}
			if up.NeedsConfirmation != tc.confirm {
				t.Errorf("NeedsConfirmation = %v, want %v (diff %+v)", up.NeedsConfirmation, tc.confirm, up.Diff)
			}
		})
	}
}

func TestPreviewUpdateOfTheInstalledCommitIsUpToDate(t *testing.T) {
	env, _ := installedV1(t)
	up, err := env.m.PreviewUpdate(context.Background(), "traffic-alerts", "")
	if err != nil {
		t.Fatal(err)
	}
	if !up.UpToDate || up.Token != "" {
		t.Errorf("preview = %+v, want up to date and nothing staged", up)
	}
	if left, _ := os.ReadDir(filepath.Join(env.codeDir, stagingDirName)); len(left) != 0 {
		t.Errorf("an up-to-date check left %d staged downloads", len(left))
	}
}

func TestPreviewUpdateAsksForTheChosenRefInTheSameRepository(t *testing.T) {
	env, _ := installedV1(t)
	env.mu.Lock()
	env.archives["/acme/alerts/tar.gz/v2.0.0"] = archiveAt(t, commitV2, "2.0.0", nil)
	env.mu.Unlock()
	up, err := env.m.PreviewUpdate(context.Background(), "traffic-alerts", "v2.0.0")
	if err != nil || up.Manifest.Version != "2.0.0" {
		t.Fatalf("preview = %+v, %v", up, err)
	}
	before := len(env.requested())
	for _, bad := range []string{"../x", "a/b", "v2 0", "-rf", "a..b", strings.Repeat("a", 300)} {
		if _, err := env.m.PreviewUpdate(context.Background(), "traffic-alerts", bad); err == nil || !strings.Contains(err.Error(), "invalid ref") {
			t.Errorf("ref %q: err = %v, want it refused as invalid before any download", bad, err)
		}
	}
	if got := len(env.requested()); got != before {
		t.Errorf("a refused ref still caused %d download(s)", got-before)
	}
	if _, err := env.m.PreviewUpdate(context.Background(), "nope", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown plugin: %v", err)
	}
}

func TestPreviewUpdateRefusesAnotherPluginAndAReplaceConflict(t *testing.T) {
	env, _ := installedV1(t)
	env.publish(archiveAt(t, commitV2, "1.3.0", func(m map[string]any) { m["id"] = "something-else" }))
	if _, err := env.m.PreviewUpdate(context.Background(), "traffic-alerts", ""); !errors.Is(err, ErrUpdateIDMismatch) {
		t.Errorf("a repository that now holds another plugin: %v, want ErrUpdateIDMismatch", err)
	}
}

func TestApplyUpdateSwapsTheCodeAndKeepsEverythingElse(t *testing.T) {
	env, proc := installedV1(t)
	ctx := context.Background()
	env.publish(archiveAt(t, commitV2, "1.3.0", func(m map[string]any) { m["permissions"] = []string{"api:clients:read"} }))
	up, err := env.m.PreviewUpdate(ctx, "traffic-alerts", "")
	if err != nil {
		t.Fatal(err)
	}

	e, err := env.m.ApplyUpdate(ctx, "traffic-alerts", up.Token, []string{"api:clients:read"})
	if err != nil {
		t.Fatal(err)
	}
	if e.Version != "1.3.0" || e.Commit != commitV2 || !e.Enabled || !SamePermissionSet(e.Permissions, []string{"api:clients:read"}) {
		t.Fatalf("entry = %+v", e)
	}
	if e.Previous == nil || e.Previous.Version != "1.2.0" || e.Previous.Commit != testCommit || !SamePermissionSet(e.Previous.Permissions, defaultPerms()) {
		t.Fatalf("previous = %+v", e.Previous)
	}
	if got := binaryOf(t, env); got != "binary 1.3.0" {
		t.Errorf("code = %q, want the new version", got)
	}
	old, err := os.ReadFile(filepath.Join(env.codeDir, "traffic-alerts.prev", "bin", "amd64", "alerts"))
	if err != nil || string(old) != "binary 1.2.0" {
		t.Errorf("kept previous code = %q, %v", old, err)
	}
	if raw, err := os.ReadFile(filepath.Join(env.dataDir, "traffic-alerts", "state.db")); err != nil || string(raw) != "data" {
		t.Errorf("plugin data touched: %q %v", raw, err)
	}
	if !slices.Equal(proc.stopped, []string{"traffic-alerts"}) {
		t.Errorf("process stops = %v, want the old process stopped once", proc.stopped)
	}
	again, err := NewManager(env.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := again.Get("traffic-alerts"); got.Version != "1.3.0" || got.Previous == nil || got.Previous.Commit != testCommit {
		t.Errorf("after a restart: %+v", got)
	}
}

func TestApplyUpdateNeedsExactlyTheNewPermissions(t *testing.T) {
	env, proc := installedV1(t)
	ctx := context.Background()
	newPerms := []string{"api:clients:read", "network"}
	env.publish(archiveAt(t, commitV2, "1.3.0", func(m map[string]any) { m["permissions"] = newPerms }))

	for name, accepted := range map[string][]string{
		"only the old ones": defaultPerms(),
		"fewer than new":    {"api:clients:read"},
		"more than new":     {"api:clients:read", "network", "api:clients:write"},
		"none":              nil,
	} {
		up, err := env.m.PreviewUpdate(ctx, "traffic-alerts", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := env.m.ApplyUpdate(ctx, "traffic-alerts", up.Token, accepted); !errors.Is(err, ErrPermissionsMismatch) {
			t.Errorf("%s: err = %v, want ErrPermissionsMismatch", name, err)
		}
		if got := binaryOf(t, env); got != "binary 1.2.0" {
			t.Fatalf("%s: code changed to %q although the permissions were not confirmed", name, got)
		}
	}
	if len(proc.stopped) != 0 {
		t.Errorf("the plugin was stopped for a refused update: %v", proc.stopped)
	}
}

func TestApplyUpdateOnlyAcceptsItsOwnFreshToken(t *testing.T) {
	env, _ := installedV1(t)
	ctx := context.Background()
	env.publish(archiveAt(t, commitV2, "1.3.0", nil))
	up, _ := env.m.PreviewUpdate(ctx, "traffic-alerts", "")

	if _, err := env.m.ApplyUpdate(ctx, "traffic-alerts", "nope", defaultPerms()); !errors.Is(err, ErrUnknownToken) {
		t.Errorf("unknown token: %v", err)
	}
	if _, err := env.m.ApplyUpdate(ctx, "other-plugin", up.Token, defaultPerms()); !errors.Is(err, ErrNotFound) {
		t.Errorf("token used for a plugin that is not installed: %v", err)
	}
	if _, err := env.m.ApplyUpdate(ctx, "traffic-alerts", up.Token, defaultPerms()); err != nil {
		t.Fatal(err)
	}
	if _, err := env.m.ApplyUpdate(ctx, "traffic-alerts", up.Token, defaultPerms()); !errors.Is(err, ErrUnknownToken) {
		t.Errorf("a token must work once: %v", err)
	}
}

func TestApplyUpdateRefusesATokenFromAnotherRepository(t *testing.T) {
	env, _ := installedV1(t)
	ctx := context.Background()
	// A different repository that happens to hold a plugin with the same id.
	env.mu.Lock()
	env.archives["/evil/alerts/tar.gz/HEAD"] = archiveAt(t, commitV2, "9.9.9", nil)
	env.mu.Unlock()
	p, err := env.m.Preview(ctx, "https://github.com/evil/alerts")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.m.ApplyUpdate(ctx, "traffic-alerts", p.Token, defaultPerms()); !errors.Is(err, ErrUpdateSourceMismatch) {
		t.Fatalf("err = %v, want ErrUpdateSourceMismatch", err)
	}
	if got := binaryOf(t, env); got != "binary 1.2.0" {
		t.Errorf("another repository replaced the plugin: %q", got)
	}
}

func TestApplyUpdateChangesNothingWhenTheOldProcessWillNotStop(t *testing.T) {
	env, proc := installedV1(t)
	ctx := context.Background()
	env.publish(archiveAt(t, commitV2, "1.3.0", nil))
	up, _ := env.m.PreviewUpdate(ctx, "traffic-alerts", "")
	proc.err = errors.New("unit is stuck")
	if _, err := env.m.ApplyUpdate(ctx, "traffic-alerts", up.Token, defaultPerms()); err == nil {
		t.Fatal("update went ahead over a process that could not be stopped")
	}
	if got := binaryOf(t, env); got != "binary 1.2.0" {
		t.Errorf("code = %q", got)
	}
	if e, _ := env.m.Get("traffic-alerts"); e.Version != "1.2.0" || e.Previous != nil {
		t.Errorf("entry changed: %+v", e)
	}
}

func TestRollbackBringsTheOldVersionBackAndKeepsData(t *testing.T) {
	env, _ := installedV1(t)
	ctx := context.Background()
	env.publish(archiveAt(t, commitV2, "1.3.0", func(m map[string]any) { m["permissions"] = []string{"api:clients:read"} }))
	up, _ := env.m.PreviewUpdate(ctx, "traffic-alerts", "")
	if _, err := env.m.ApplyUpdate(ctx, "traffic-alerts", up.Token, []string{"api:clients:read"}); err != nil {
		t.Fatal(err)
	}

	e, err := env.m.Rollback(ctx, "traffic-alerts")
	if err != nil {
		t.Fatal(err)
	}
	if e.Version != "1.2.0" || e.Commit != testCommit || e.Previous != nil || !e.Enabled || !SamePermissionSet(e.Permissions, defaultPerms()) {
		t.Fatalf("entry = %+v", e)
	}
	if got := binaryOf(t, env); got != "binary 1.2.0" {
		t.Errorf("code = %q", got)
	}
	if _, err := os.Stat(filepath.Join(env.codeDir, "traffic-alerts.prev")); !os.IsNotExist(err) {
		t.Errorf("the rolled-back copy is still around: %v", err)
	}
	if raw, err := os.ReadFile(filepath.Join(env.dataDir, "traffic-alerts", "state.db")); err != nil || string(raw) != "data" {
		t.Errorf("plugin data touched: %q %v", raw, err)
	}
	if _, err := env.m.Rollback(ctx, "traffic-alerts"); !errors.Is(err, ErrNoPreviousVersion) {
		t.Errorf("second rollback: %v, want ErrNoPreviousVersion", err)
	}
}

func TestRollbackWithoutTheSavedCodeChangesNothing(t *testing.T) {
	env, _ := installedV1(t)
	ctx := context.Background()
	env.publish(archiveAt(t, commitV2, "1.3.0", nil))
	up, _ := env.m.PreviewUpdate(ctx, "traffic-alerts", "")
	if _, err := env.m.ApplyUpdate(ctx, "traffic-alerts", up.Token, defaultPerms()); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(env.codeDir, "traffic-alerts.prev")); err != nil {
		t.Fatal(err)
	}
	if _, err := env.m.Rollback(ctx, "traffic-alerts"); err == nil {
		t.Fatal("rolled back to code that is gone")
	}
	if got := binaryOf(t, env); got != "binary 1.3.0" {
		t.Errorf("code = %q, the current version must stay", got)
	}
	if e, _ := env.m.Get("traffic-alerts"); e.Version != "1.3.0" {
		t.Errorf("entry = %+v", e)
	}
}

func TestOnlyTheLastVersionIsKeptForRollback(t *testing.T) {
	env, _ := installedV1(t)
	ctx := context.Background()
	for _, step := range []struct{ commit, version string }{{commitV2, "1.3.0"}, {commitV3, "1.4.0"}} {
		env.publish(archiveAt(t, step.commit, step.version, nil))
		up, err := env.m.PreviewUpdate(ctx, "traffic-alerts", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := env.m.ApplyUpdate(ctx, "traffic-alerts", up.Token, defaultPerms()); err != nil {
			t.Fatal(err)
		}
	}
	e, _ := env.m.Get("traffic-alerts")
	if e.Version != "1.4.0" || e.Previous == nil || e.Previous.Version != "1.3.0" {
		t.Fatalf("entry = %+v", e)
	}
	rolled, err := env.m.Rollback(ctx, "traffic-alerts")
	if err != nil || rolled.Version != "1.3.0" {
		t.Fatalf("rollback = %+v, %v", rolled, err)
	}
}

func TestUninstallRemovesTheSavedVersionToo(t *testing.T) {
	env, _ := installedV1(t)
	ctx := context.Background()
	env.publish(archiveAt(t, commitV2, "1.3.0", nil))
	up, _ := env.m.PreviewUpdate(ctx, "traffic-alerts", "")
	if _, err := env.m.ApplyUpdate(ctx, "traffic-alerts", up.Token, defaultPerms()); err != nil {
		t.Fatal(err)
	}
	if err := env.m.Uninstall("traffic-alerts", false); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"traffic-alerts", "traffic-alerts.prev"} {
		if _, err := os.Stat(filepath.Join(env.codeDir, name)); !os.IsNotExist(err) {
			t.Errorf("%s survived the uninstall: %v", name, err)
		}
	}
}

func TestApplyUpdateRefusesAPreviewOfAnotherPluginInTheSameRepository(t *testing.T) {
	env, _ := installedV1(t)
	ctx := context.Background()
	env.publish(archiveAt(t, commitV2, "1.3.0", func(m map[string]any) { m["id"] = "something-else" }))
	p, err := env.m.Preview(ctx, updURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.m.ApplyUpdate(ctx, "traffic-alerts", p.Token, p.Manifest.Permissions); !errors.Is(err, ErrUpdateIDMismatch) {
		t.Fatalf("err = %v, want ErrUpdateIDMismatch", err)
	}
	if got := binaryOf(t, env); got != "binary 1.2.0" {
		t.Errorf("code = %q", got)
	}
}
