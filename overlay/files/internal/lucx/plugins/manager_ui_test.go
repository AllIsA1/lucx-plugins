// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func pageArchive(t *testing.T, id string, replaces ...string) []byte {
	t.Helper()
	return goodArchive(t, func(m map[string]any) {
		m["id"] = id
		m["type"] = "ui"
		delete(m, "run")
		perms := []string{"ui:menu"}
		for _, r := range replaces {
			perms = append(perms, "ui:replace:"+r)
		}
		m["permissions"] = perms
		m["ui"] = map[string]any{
			"menu":     []map[string]any{{"title": "Page " + id, "icon": "bell", "path": "/ui/index.html"}},
			"replaces": replaces,
		}
	})
}

func pagePerms(replaces ...string) []string {
	perms := []string{"ui:menu"}
	for _, r := range replaces {
		perms = append(perms, "ui:replace:"+r)
	}
	return perms
}

func TestInstallRecordsWhatThePluginAddsToTheInterface(t *testing.T) {
	env := newEnv(t, map[string][]byte{"/acme/alerts/tar.gz/HEAD": pageArchive(t, "page-a", "/clients")})
	p, err := env.m.Preview(context.Background(), alertsURL)
	if err != nil {
		t.Fatal(err)
	}
	e, err := env.m.Install(p.Token, pagePerms("/clients"))
	if err != nil {
		t.Fatal(err)
	}
	if len(e.UI.Menu) != 1 || e.UI.Menu[0].Title != "Page page-a" || e.UI.Menu[0].Path != "/ui/index.html" || len(e.UI.Replaces) != 1 || e.UI.Replaces[0] != "/clients" {
		t.Fatalf("entry UI = %+v", e.UI)
	}
	again, err := NewManager(env.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := again.reg.Get("page-a"); len(got.UI.Menu) != 1 || len(got.UI.Replaces) != 1 {
		t.Errorf("UI lost across a restart: %+v", got.UI)
	}
}

func TestAPageHasOnlyOnePluginReplacingIt(t *testing.T) {
	env := newEnv(t, map[string][]byte{
		"/acme/alerts/tar.gz/HEAD": pageArchive(t, "page-a", "/clients"),
		"/acme/other/tar.gz/HEAD":  pageArchive(t, "page-b", "/clients", "/hosts"),
	})
	ctx := context.Background()
	first, err := env.m.Preview(ctx, alertsURL)
	if err != nil {
		t.Fatal(err)
	}
	// Both are previewed while the page is still free; the conflict must still be caught at install.
	second, err := env.m.Preview(ctx, "https://github.com/acme/other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.m.Install(first.Token, pagePerms("/clients")); err != nil {
		t.Fatal(err)
	}
	if _, err := env.m.Install(second.Token, pagePerms("/clients", "/hosts")); !errors.Is(err, ErrReplaceConflict) {
		t.Fatalf("install of a second replacement = %v, want ErrReplaceConflict", err)
	}
	if _, ok := env.m.reg.Get("page-b"); ok {
		t.Error("the conflicting plugin was installed anyway")
	}

	_, err = env.m.Preview(ctx, "https://github.com/acme/other")
	if !errors.Is(err, ErrReplaceConflict) || !strings.Contains(err.Error(), "page-a") || !strings.Contains(err.Error(), "/clients") {
		t.Fatalf("preview = %v, want a conflict naming page-a and /clients", err)
	}
}

func TestPreviewOfAnInstalledPluginIsNotAConflictWithItself(t *testing.T) {
	env := newEnv(t, map[string][]byte{"/acme/alerts/tar.gz/HEAD": pageArchive(t, "page-a", "/clients")})
	ctx := context.Background()
	p, _ := env.m.Preview(ctx, alertsURL)
	if _, err := env.m.Install(p.Token, pagePerms("/clients")); err != nil {
		t.Fatal(err)
	}
	again, err := env.m.Preview(ctx, alertsURL)
	if err != nil || !again.Installed {
		t.Fatalf("preview of the installed plugin = %+v, %v", again, err)
	}
}

func TestPageTokensFollowThePluginAndTheClock(t *testing.T) {
	env, id := uiPluginEnv(t, pageFiles, nil)
	tok, expires, err := env.m.IssueFrameToken(id, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if want := env.clock().Add(FrameTokenTTL); !expires.Equal(want) {
		t.Errorf("expires = %v, want %v", expires, want)
	}
	if e, user, ok := env.m.VerifyFrameToken(tok); !ok || e.ID != id || user != "admin" {
		t.Fatalf("VerifyFrameToken = %+v %q %v", e, user, ok)
	}

	env.advance(FrameTokenTTL + time.Second)
	if _, _, ok := env.m.VerifyFrameToken(tok); ok {
		t.Error("an expired page token worked")
	}
	env.advance(-FrameTokenTTL)
	if err := env.m.SetEnabled(id, false); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := env.m.VerifyFrameToken(tok); ok {
		t.Error("a page token outlived disabling the plugin")
	}
}

func TestPageTokensAreOnlyForPluginsThatShowAPage(t *testing.T) {
	env, id := uiPluginEnv(t, pageFiles, nil)
	if _, _, err := env.m.IssueFrameToken("nope", "admin"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown plugin: %v, want ErrNotFound", err)
	}
	if err := env.m.SetEnabled(id, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := env.m.IssueFrameToken(id, "admin"); !errors.Is(err, ErrNotRunning) {
		t.Errorf("disabled plugin: %v, want ErrNotRunning", err)
	}

	nodeOnly, nodeID := uiPluginEnv(t, pageFiles, func(m map[string]any) { m["scope"] = "node" })
	if _, _, err := nodeOnly.m.IssueFrameToken(nodeID, "admin"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a node plugin on a master: %v, want ErrNotFound", err)
	}

	process := newEnv(t, map[string][]byte{"/acme/alerts/tar.gz/HEAD": goodArchive(t, nil)})
	p, _ := process.m.Preview(context.Background(), alertsURL)
	if _, err := process.m.Install(p.Token, defaultPerms()); err != nil {
		t.Fatal(err)
	}
	_ = process.m.SetEnabled("traffic-alerts", true)
	if _, _, err := process.m.IssueFrameToken("traffic-alerts", "admin"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a plugin that adds no page: %v, want ErrNotFound", err)
	}
}
