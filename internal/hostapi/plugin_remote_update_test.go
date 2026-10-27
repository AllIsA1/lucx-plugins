// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/AllIsA1/lucx-plugins/internal/plugins"
)

func pinnedManifest(version string) string {
	return `{"id":"traffic-alerts","name":"Traffic Alerts","version":"` + version + `","apiVersion":"1","type":"process",` +
		`"run":{"command":"bin/${arch}/alerts"},"permissions":["api:clients:read"],"scope":"both"}`
}

func pinnedReq(manifest, commit string) plugins.PinnedInstall {
	sum := sha256.Sum256([]byte(manifest))
	return plugins.PinnedInstall{
		URL: "https://github.com/acme/alerts", Commit: commit,
		ManifestSHA256: hex.EncodeToString(sum[:]), Permissions: []string{"api:clients:read"},
	}
}

// A node's plugin that others require cannot be removed, so the master's update must replace it where it stands.
func TestAPinnedUpdateReplacesThePluginInPlaceAndKeepsItsState(t *testing.T) {
	var mu sync.Mutex
	served := pluginsArchiveAt(t, pinnedManifest("1.2.0"), pluginsTestCommit)
	m := newManagerServingFunc(t, func() []byte { mu.Lock(); defer mu.Unlock(); return served })
	svc := &PluginService{Provider: func() (*plugins.Manager, error) { return m, nil }}
	ctx := context.Background()

	if _, err := m.InstallPinned(ctx, pinnedReq(pinnedManifest("1.2.0"), pluginsTestCommit)); err != nil {
		t.Fatal(err)
	}
	if err := m.SetEnabled("traffic-alerts", true); err != nil {
		t.Fatal(err)
	}

	// nothing new yet
	if _, err := svc.UpdatePinned(ctx, pinnedReq(pinnedManifest("1.2.0"), pluginsTestCommit)); !errors.Is(err, plugins.ErrAlreadyCurrent) {
		t.Errorf("same commit: %v, want ErrAlreadyCurrent", err)
	}

	mu.Lock()
	served = pluginsArchiveAt(t, pinnedManifest("1.3.0"), updateCommit2)
	mu.Unlock()

	// the master approved another manifest than the node would download
	bad := pinnedReq(pinnedManifest("1.3.0"), updateCommit2)
	bad.ManifestSHA256 = strings.Repeat("0", 64)
	if _, err := svc.UpdatePinned(ctx, bad); !errors.Is(err, plugins.ErrManifestMismatch) {
		t.Errorf("wrong manifest hash: %v, want ErrManifestMismatch", err)
	}
	if e := m.List()[0]; e.Version != "1.2.0" {
		t.Fatalf("a refused update changed the plugin: %+v", e)
	}

	entry, err := svc.UpdatePinned(ctx, pinnedReq(pinnedManifest("1.3.0"), updateCommit2))
	if err != nil {
		t.Fatal(err)
	}
	list := m.List()
	if len(list) != 1 || entry.Version != "1.3.0" || list[0].Commit != updateCommit2 || !list[0].ManagedByMaster || !list[0].Enabled || list[0].Previous == nil {
		t.Errorf("after the update: %+v (returned %+v)", list, entry)
	}
}

func TestAPinnedUpdateTouchesOnlyWhatTheMasterManages(t *testing.T) {
	served := pluginsArchiveAt(t, pinnedManifest("1.2.0"), pluginsTestCommit)
	m := newManagerServingFunc(t, func() []byte { return served })
	svc := &PluginService{Provider: func() (*plugins.Manager, error) { return m, nil }}
	ctx := context.Background()
	req := pinnedReq(pinnedManifest("1.2.0"), pluginsTestCommit)

	if _, err := svc.UpdatePinned(ctx, req); !errors.Is(err, plugins.ErrNotFound) {
		t.Errorf("not installed: %v, want ErrNotFound", err)
	}

	// installed by the node owner from a link: not the master's
	p := previewOK(t, newPluginsHarnessWith(t, svc))
	if _, err := m.Install(p.Token, p.Manifest.Permissions); err != nil {
		t.Fatal(err)
	}
	served = pluginsArchiveAt(t, pinnedManifest("1.3.0"), updateCommit2)
	if _, err := svc.UpdatePinned(ctx, pinnedReq(pinnedManifest("1.3.0"), updateCommit2)); !errors.Is(err, plugins.ErrNotMasterManaged) {
		t.Errorf("installed by hand: %v, want ErrNotMasterManaged", err)
	}
	if e := m.List()[0]; e.Commit != pluginsTestCommit {
		t.Errorf("a plugin the node owner installed was updated by the master: %+v", e)
	}
}

func TestTheRemoteUpdateRouteIsOpenToAMasterOnlyOnceTheNodeAllowedIt(t *testing.T) {
	served := pluginsArchiveAt(t, pinnedManifest("1.2.0"), pluginsTestCommit)
	m := newManagerServingFunc(t, func() []byte { return served })
	master := newScopedHarness(t, scopeNodeSync, m, nil)
	body := map[string]any{
		"url": "https://github.com/acme/alerts", "commit": pluginsTestCommit,
		"manifestSha256": strings.Repeat("0", 64), "permissions": []string{"api:clients:read"},
	}
	if r := master.callAt(t, http.MethodPost, "/plugins/remote/update", body); r.Success || !strings.Contains(r.Msg, "disabled on this node") {
		t.Errorf("a locked-out master: %+v", r)
	}
	if err := m.SetAllowMasterManagement(true); err != nil {
		t.Fatal(err)
	}
	if r := master.callAt(t, http.MethodPost, "/plugins/remote/update", body); r.Success {
		t.Error("an update of a plugin that is not installed was accepted")
	}
	if r := master.callAt(t, http.MethodPost, "/plugins/remote/update", "not json"); r.Success {
		t.Error("a malformed body was accepted")
	}
}
