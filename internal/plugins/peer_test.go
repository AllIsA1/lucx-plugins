// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPeerPermissionIsValidDangerousAndNeedsAProcess(t *testing.T) {
	if err := ValidatePermission("peer:better-cores"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"peer:", "peer:A", "peer:x", "peer:-ab", "peer:a/b", "peer:better-cores/x"} {
		if ValidatePermission(bad) == nil {
			t.Errorf("%q must not be a permission", bad)
		}
	}
	if !IsDangerousPermission("peer:better-cores") {
		t.Error("calling another plugin reaches whatever that plugin allows, so it has to be confirmed like any dangerous permission")
	}
	if id, ok := PeerOf("peer:better-cores"); !ok || id != "better-cores" {
		t.Errorf("PeerOf = %q %v", id, ok)
	}
	if _, ok := PeerOf("network"); ok {
		t.Error("network is not a peer permission")
	}
}

func TestManifestPeerPermissionNeedsTheRequirement(t *testing.T) {
	base := func(perms []string, requires []string) string {
		quote := func(list []string) string {
			parts := make([]string, len(list))
			for i, s := range list {
				parts[i] = `"` + s + `"`
			}
			return "[" + strings.Join(parts, ",") + "]"
		}
		return `{"id":"better-warp","name":"W","version":"1.0.0","apiVersion":"1","type":"process","run":{"command":"bin/${arch}/w"},` +
			`"permissions":` + quote(perms) + `,"requires":` + quote(requires) + `}`
	}
	if _, err := ParseManifest([]byte(base([]string{"peer:better-cores"}, []string{"better-cores"}))); err != nil {
		t.Fatalf("a required peer: %v", err)
	}
	if _, err := ParseManifest([]byte(base([]string{"peer:better-cores"}, nil))); err == nil || !strings.Contains(err.Error(), "requires") {
		t.Errorf("a peer that is not required must be refused, got %v", err)
	}
	if _, err := ParseManifest([]byte(base([]string{"peer:better-warp"}, []string{"better-cores"}))); err == nil {
		t.Error("a plugin must not hold the permission to call itself")
	}
	ui := strings.Replace(base([]string{"peer:better-cores"}, []string{"better-cores"}), `"type":"process","run":{"command":"bin/${arch}/w"}`, `"type":"ui"`, 1)
	if _, err := ParseManifest([]byte(ui)); err == nil {
		t.Error("a ui plugin has no process and cannot call a peer")
	}
}

func TestProxyPeerReachesOnlyThePeerPrefixAndNamesTheCaller(t *testing.T) {
	e := newProxyEnv(t, nil)
	do := func(subpath string, hdr map[string]string) (*httptest.ResponseRecorder, error) {
		req := httptest.NewRequest(http.MethodPost, "/anything?x=1", strings.NewReader("payload"))
		req.Header.Set("Cookie", "3x-ui=session-secret")
		req.Header.Set("Authorization", "Bearer plugin-token")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		return w, e.sup.ProxyPeer(w, req, "better-warp", "traffic-alerts", subpath)
	}

	w, err := do("/warp/check", map[string]string{"X-LucX-Caller": "someone-else", "X-LucX-User": "admin"})
	if err != nil || w.Code != http.StatusOK {
		t.Fatalf("ProxyPeer: %v %d", err, w.Code)
	}
	got := e.last(t)
	if got.Path != "/peer/warp/check" || got.Query != "x=1" || got.Body != "payload" || got.Method != http.MethodPost {
		t.Errorf("forwarded request = %+v", got)
	}
	if got.Header.Get("X-Lucx-Caller") != "better-warp" || got.Header.Get("X-Lucx-Plugin-Id") != "traffic-alerts" {
		t.Errorf("the peer must learn the caller from the host alone: %v", got.Header)
	}
	if got.Header.Get("X-Lucx-User") != "" || got.Header.Get("Cookie") != "" || got.Header.Get("Authorization") != "" {
		t.Errorf("a peer call carries no user and no credentials: %v", got.Header)
	}

	// Nothing outside /peer is reachable, however the path is spelled.
	for _, p := range []string{"", "/", "/../ui/index.html", "/a/../../_lucx/health", "/..%2f_lucx"} {
		before := len(e.seen)
		if _, err := do(p, nil); !errors.Is(err, ErrNotFound) {
			t.Errorf("%q: err = %v, want ErrNotFound", p, err)
		}
		if len(e.seen) != before {
			t.Errorf("%q reached the plugin", p)
		}
	}
	// A plugin cannot call itself, and an unknown plugin is not found.
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if err := e.sup.ProxyPeer(httptest.NewRecorder(), req, "traffic-alerts", "traffic-alerts", "/a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("self call: %v", err)
	}
	if err := e.sup.ProxyPeer(httptest.NewRecorder(), req, "better-warp", "missing", "/a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown peer: %v", err)
	}
}

func TestProxyPeerRefusesAPeerThatIsOff(t *testing.T) {
	e := newProxyEnv(t, nil)
	if err := e.m.SetEnabled("traffic-alerts", false); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if err := e.sup.ProxyPeer(httptest.NewRecorder(), req, "better-warp", "traffic-alerts", "/a"); !errors.Is(err, ErrNotRunning) {
		t.Errorf("a peer that is off: %v", err)
	}
}
