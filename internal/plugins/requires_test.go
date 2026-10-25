// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const baseURL = "https://github.com/acme/base"

func baseArchive(t *testing.T) []byte {
	t.Helper()
	return buildArchive(t, testCommit,
		dir("base-"+testCommit[:7]+"/"),
		file("base-"+testCommit[:7]+"/lucx-plugin.json", string(manifestJSON(t, func(m map[string]any) { m["id"], m["name"] = "base-core", "Base" }))),
		file("base-"+testCommit[:7]+"/bin/amd64/alerts", "ELF"),
	)
}

func requiringBase(m map[string]any) { m["requires"] = []any{"base-core"} }

func requiresEnv(t *testing.T) *testEnv {
	t.Helper()
	return newEnv(t, map[string][]byte{
		"/acme/alerts/tar.gz/HEAD": archiveAt(t, testCommit, "1.2.0", requiringBase),
		"/acme/base/tar.gz/HEAD":   baseArchive(t),
	})
}

func installURL(t *testing.T, env *testEnv, url string) error {
	t.Helper()
	p, err := env.m.Preview(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	_, err = env.m.Install(p.Token, p.Manifest.Permissions)
	return err
}

func TestAPluginThatRequiresAnotherWaitsForItToBeInstalledAndTurnedOn(t *testing.T) {
	env := requiresEnv(t)
	if err := installURL(t, env, alertsURL); !errors.Is(err, ErrRequirementMissing) || !strings.Contains(err.Error(), "base-core") {
		t.Fatalf("without the base: %v", err)
	}
	if err := installURL(t, env, baseURL); err != nil {
		t.Fatal(err)
	}
	if err := installURL(t, env, alertsURL); !errors.Is(err, ErrRequirementMissing) {
		t.Fatalf("with the base installed but off: %v", err)
	}
	if err := env.m.SetEnabled("base-core", true); err != nil {
		t.Fatal(err)
	}
	if err := installURL(t, env, alertsURL); err != nil {
		t.Fatalf("with the base on: %v", err)
	}
	if have, _ := env.m.reg.Get("traffic-alerts"); len(have.Requires) != 1 || have.Requires[0] != "base-core" {
		t.Errorf("requires = %v, want it kept in the registry", have.Requires)
	}
}

func TestTurningOnNeedsTheRequirementAndTurningOffOrRemovingNeedsNoUser(t *testing.T) {
	env := requiresEnv(t)
	if err := installURL(t, env, baseURL); err != nil {
		t.Fatal(err)
	}
	if err := env.m.SetEnabled("base-core", true); err != nil {
		t.Fatal(err)
	}
	if err := installURL(t, env, alertsURL); err != nil {
		t.Fatal(err)
	}
	if err := env.m.SetEnabled("traffic-alerts", true); err != nil {
		t.Fatal(err)
	}
	if err := env.m.SetEnabled("base-core", false); !errors.Is(err, ErrRequiredBy) || !strings.Contains(err.Error(), "traffic-alerts") {
		t.Errorf("turning the base off under a plugin that is on: %v", err)
	}
	if err := env.m.Uninstall("base-core", false); !errors.Is(err, ErrRequiredBy) {
		t.Errorf("removing the base under a plugin: %v", err)
	}
	if err := env.m.SetEnabled("traffic-alerts", false); err != nil {
		t.Fatal(err)
	}
	// the user is off now: the base may go off, but the user cannot come back on
	if err := env.m.SetEnabled("base-core", false); err != nil {
		t.Errorf("turning the base off with no plugin on: %v", err)
	}
	if err := env.m.SetEnabled("traffic-alerts", true); !errors.Is(err, ErrRequirementMissing) {
		t.Errorf("turning the user on without the base: %v", err)
	}
	if err := env.m.Uninstall("base-core", false); !errors.Is(err, ErrRequiredBy) {
		t.Errorf("removing the base while a plugin still needs it (even off): %v", err)
	}
	if err := env.m.Uninstall("traffic-alerts", false); err != nil {
		t.Fatal(err)
	}
	if err := env.m.Uninstall("base-core", false); err != nil {
		t.Errorf("removing the base once nothing needs it: %v", err)
	}
}

func TestManifestRequiresIsChecked(t *testing.T) {
	for name, requires := range map[string][]any{
		"itself":   {"traffic-alerts"},
		"twice":    {"base-core", "base-core"},
		"bad id":   {"Not An Id"},
		"too many": {"aaa", "bbb", "ccc", "ddd", "eee", "fff", "ggg", "hhh", "iii"},
	} {
		raw := manifestJSON(t, func(m map[string]any) { m["requires"] = requires })
		if _, err := ParseManifest(raw); err == nil {
			t.Errorf("%s: the manifest was accepted", name)
		}
	}
	if _, err := ParseManifest(manifestJSON(t, requiringBase)); err != nil {
		t.Errorf("a plain requirement: %v", err)
	}
}

func TestAnUpdateThatNeedsMoreThanIsThereIsRefusedWhileThePluginIsOn(t *testing.T) {
	env := newEnv(t, map[string][]byte{"/acme/alerts/tar.gz/HEAD": archiveAt(t, testCommit, "1.2.0", nil)})
	ctx := context.Background()
	if err := installURL(t, env, alertsURL); err != nil {
		t.Fatal(err)
	}
	if err := env.m.SetEnabled("traffic-alerts", true); err != nil {
		t.Fatal(err)
	}
	env.archives["/acme/alerts/tar.gz/HEAD"] = archiveAt(t, commitV2, "1.3.0", requiringBase)
	up, err := env.m.PreviewUpdate(ctx, "traffic-alerts", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.m.ApplyUpdate(ctx, "traffic-alerts", up.Token, up.Manifest.Permissions); !errors.Is(err, ErrRequirementMissing) {
		t.Errorf("apply: %v, want ErrRequirementMissing", err)
	}
}

func TestAnUpdateThatStartsToRequireAPluginMakesItNeededFromThenOn(t *testing.T) {
	env := requiresEnv(t)
	ctx := context.Background()
	env.archives["/acme/alerts/tar.gz/HEAD"] = archiveAt(t, testCommit, "1.2.0", nil)
	for _, u := range []string{baseURL, alertsURL} {
		if err := installURL(t, env, u); err != nil {
			t.Fatal(err)
		}
		if u == baseURL {
			if err := env.m.SetEnabled("base-core", true); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := env.m.SetEnabled("traffic-alerts", true); err != nil {
		t.Fatal(err)
	}
	env.archives["/acme/alerts/tar.gz/HEAD"] = archiveAt(t, commitV2, "1.3.0", requiringBase)
	up, err := env.m.PreviewUpdate(ctx, "traffic-alerts", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.m.ApplyUpdate(ctx, "traffic-alerts", up.Token, up.Manifest.Permissions); err != nil {
		t.Fatal(err)
	}
	if err := env.m.SetEnabled("base-core", false); !errors.Is(err, ErrRequiredBy) {
		t.Errorf("turning the base off after the update: %v", err)
	}
}
