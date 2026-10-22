// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package controller

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/lucx/plugins"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

type catalogFixture struct {
	h    *pluginsHarness
	m    *plugins.Manager
	priv ed25519.PrivateKey
	mu   sync.Mutex
	idx  []byte
	sig  string
}

func (f *catalogFixture) publish(t *testing.T, sequence int, entries ...map[string]any) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"schema": 1, "sequence": sequence, "generatedAt": "2026-10-19T12:00:00Z", "plugins": entries,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.idx, f.sig = raw, plugins.SignCatalog(f.priv, raw)
}

func catalogEntryOf(manifest, commit string) map[string]any {
	sum := sha256.Sum256([]byte(manifest))
	return map[string]any{
		"id": "traffic-alerts", "name": "Traffic Alerts", "summary": "Alerts on traffic", "repo": "https://github.com/acme/alerts",
		"commit": commit, "version": "1.2.0", "manifestSha256": hex.EncodeToString(sum[:]),
		"type": "process", "scope": "master", "permissions": []string{"api:clients:read"},
		"icon": "bell", "license": "MIT", "verified": map[string]any{"by": "LucX", "at": "2026-10-10"},
	}
}

func newCatalogFixture(t *testing.T, configured bool) *catalogFixture {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f := &catalogFixture{priv: priv}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/" + plugins.CatalogIndexFile:
			_, _ = w.Write(f.idx)
		case "/" + plugins.CatalogSigFile:
			_, _ = w.Write([]byte(f.sig))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	archive := pluginsArchiveWithManifest(t, updateV1)
	f.m = newManagerServingFunc(t, func() []byte { return archive }, func(c *plugins.Config) {
		if configured {
			c.CatalogKeys = []ed25519.PublicKey{pub}
		}
		c.CatalogBase = srv.URL
	})
	f.h = frameHarnessWith(t, &service.PluginService{Provider: func() (*plugins.Manager, error) { return f.m, nil }})
	return f
}

const catalogAPI = "/panel/api/plugins/catalog"

func (f *catalogFixture) view(t *testing.T, refresh bool) plugins.CatalogView {
	t.Helper()
	r := f.h.call(t, http.MethodGet, catalogAPI, nil)
	if refresh {
		r = f.h.call(t, http.MethodPost, catalogAPI+"/refresh", map[string]string{})
	}
	if !r.Success {
		t.Fatalf("catalog: %s", r.Msg)
	}
	var v plugins.CatalogView
	if err := json.Unmarshal(r.Obj, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestCatalogRouteListsWhatTheSignedIndexSays(t *testing.T) {
	f := newCatalogFixture(t, true)
	f.publish(t, 3, catalogEntryOf(updateV1, pluginsTestCommit))
	v := f.view(t, false)
	if v.State != plugins.CatalogOK || len(v.Plugins) != 1 || v.Plugins[0].Status != plugins.CatalogAvailable || v.Plugins[0].Verified == nil {
		t.Fatalf("view = %+v", v)
	}
	if v.Plugins[0].Name != "Traffic Alerts" || v.Sequence != 3 {
		t.Errorf("plugin = %+v", v.Plugins[0])
	}
}

func TestCatalogRouteWithoutAKeyIsUnconfigured(t *testing.T) {
	f := newCatalogFixture(t, false)
	if v := f.view(t, false); v.State != plugins.CatalogUnconfigured || len(v.Plugins) != 0 {
		t.Errorf("view = %+v", v)
	}
}

func TestInstallFromTheCatalogGoesThroughThePinnedCommit(t *testing.T) {
	f := newCatalogFixture(t, true)
	f.publish(t, 3, catalogEntryOf(updateV1, pluginsTestCommit))
	f.view(t, false)

	r := f.h.call(t, http.MethodPost, catalogAPI+"/preview/traffic-alerts", map[string]string{})
	if !r.Success {
		t.Fatalf("preview: %s", r.Msg)
	}
	var p plugins.Preview
	if err := json.Unmarshal(r.Obj, &p); err != nil || p.Catalog != plugins.ListingListed || p.Commit != pluginsTestCommit {
		t.Fatalf("preview = %+v (%v)", p, err)
	}
	if r := f.h.call(t, http.MethodPost, "/panel/api/plugins/install", map[string]any{"token": p.Token, "permissions": p.Manifest.Permissions}); !r.Success {
		t.Fatalf("install: %s", r.Msg)
	}
	if v := f.view(t, false); v.Plugins[0].Status != plugins.CatalogInstalled {
		t.Errorf("status after install = %q", v.Plugins[0].Status)
	}
}

func TestInstallFromTheCatalogRefusesAnArchiveTheIndexDidNotSign(t *testing.T) {
	f := newCatalogFixture(t, true)
	entry := catalogEntryOf(updateV1, pluginsTestCommit)
	entry["manifestSha256"] = strings.Repeat("0", 64)
	f.publish(t, 3, entry)
	f.view(t, false)
	if r := f.h.call(t, http.MethodPost, catalogAPI+"/preview/traffic-alerts", map[string]string{}); r.Success || !strings.Contains(r.Msg, "does not match") {
		t.Fatalf("preview of an archive that differs from the index = %+v", r)
	}
	if r := f.h.call(t, http.MethodPost, catalogAPI+"/preview/not-listed", map[string]string{}); r.Success {
		t.Error("a plugin that is not in the catalog could be previewed from it")
	}
}

func TestACatalogRollbackIsRefusedAndTheOldCatalogStays(t *testing.T) {
	f := newCatalogFixture(t, true)
	f.publish(t, 9, catalogEntryOf(updateV1, pluginsTestCommit))
	f.view(t, false)
	f.publish(t, 4)
	v := f.view(t, true)
	if v.State != plugins.CatalogStale || len(v.Plugins) != 1 || !strings.Contains(v.Error, "older") {
		t.Errorf("view = %+v", v)
	}
}

func TestCatalogSourceSettingRejectsInsecureMirrors(t *testing.T) {
	f := newCatalogFixture(t, true)
	if r := f.h.call(t, http.MethodPost, catalogAPI+"/settings", map[string]string{"base": "http://evil.example"}); r.Success {
		t.Fatal("a plain-http mirror was accepted")
	}
	f.publish(t, 1)
	if r := f.h.call(t, http.MethodPost, catalogAPI+"/settings", map[string]string{"base": "https://mirror.example/index"}); !r.Success {
		t.Fatalf("a good mirror: %s", r.Msg)
	}
	if v := f.view(t, false); v.CustomBase != "https://mirror.example/index" {
		t.Errorf("custom base = %q", v.CustomBase)
	}
}

func TestLinkPreviewReportsWhetherTheCatalogKnowsTheCommit(t *testing.T) {
	f := newCatalogFixture(t, true)
	f.publish(t, 3, catalogEntryOf(updateV1, "9999999999999999999999999999999999999999"))
	f.view(t, false)
	r := f.h.call(t, http.MethodPost, "/panel/api/plugins/preview", map[string]string{"url": "https://github.com/acme/alerts"})
	var p plugins.Preview
	if err := json.Unmarshal(r.Obj, &p); err != nil || p.Catalog != plugins.ListingUnlisted {
		t.Fatalf("preview = %+v (%v)", p, err)
	}
}
