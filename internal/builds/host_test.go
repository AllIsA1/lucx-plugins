// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package builds

import (
	"crypto/ed25519"
	"strings"
	"testing"
)

var hostHashes = map[string]string{"amd64": hashA, "arm64": hashB}

func hostIndex(t *testing.T, version string) *HostIndex {
	t.Helper()
	h := &HostIndex{Schema: HostSchemaVersion, Sequence: 1}
	if err := h.Set(version, hostHashes); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestHostDocumentRoundTripsAndNamesItsDownloads(t *testing.T) {
	h := hostIndex(t, "host-3")
	raw, err := MarshalHost(h)
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseHost(raw)
	if err != nil {
		t.Fatalf("our own document does not parse: %v", err)
	}
	again, _ := MarshalHost(back)
	if string(raw) != string(again) {
		t.Error("marshal, parse, marshal is not stable")
	}
	b, ok := back.Binary("arm64")
	if !ok || b.URL != "https://github.com/AllIsA1/lucx-plugins/releases/download/host-3/lucx-plugin-host-linux-arm64" || b.SHA256 != hashB {
		t.Errorf("arm64 binary = %+v", b)
	}
	if _, ok := back.Binary("riscv64"); ok {
		t.Error("found a binary for an architecture nobody built")
	}
}

func TestHostValidateRejectsWhatTheClientMustNotTrust(t *testing.T) {
	cases := map[string]func(h *HostIndex){
		"wrong schema":         func(h *HostIndex) { h.Schema = 2 },
		"zero sequence":        func(h *HostIndex) { h.Sequence = 0 },
		"not a host version":   func(h *HostIndex) { h.Version = "v3.10.2-lucx.307" },
		"leading zero":         func(h *HostIndex) { h.Version = "host-03" },
		"host-0":               func(h *HostIndex) { h.Version = "host-0" },
		"one architecture":     func(h *HostIndex) { h.Binaries = h.Binaries[:1] },
		"wrong order":          func(h *HostIndex) { h.Binaries[0], h.Binaries[1] = h.Binaries[1], h.Binaries[0] },
		"uppercase hash":       func(h *HostIndex) { h.Binaries[0].SHA256 = strings.Repeat("A", 64) },
		"short hash":           func(h *HostIndex) { h.Binaries[0].SHA256 = "abc" },
		"same hash twice":      func(h *HostIndex) { h.Binaries[1].SHA256 = h.Binaries[0].SHA256 },
		"foreign download url": func(h *HostIndex) { h.Binaries[0].URL = "https://evil.example/host" },
		"url of another tag":   func(h *HostIndex) { h.Binaries[0].URL = strings.Replace(h.Binaries[0].URL, "host-3", "host-2", 1) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			h := hostIndex(t, "host-3")
			mutate(h)
			if err := h.Validate(); err == nil {
				t.Error("accepted")
			}
		})
	}
}

func TestParseHostIsStrict(t *testing.T) {
	good, _ := MarshalHost(hostIndex(t, "host-1"))
	if _, err := ParseHost(append(append([]byte(nil), good...), []byte(`{}`)...)); err == nil {
		t.Error("trailing data accepted")
	}
	if _, err := ParseHost([]byte(strings.Replace(string(good), `"schema": 1,`, `"schema": 1, "extra": true,`, 1))); err == nil {
		t.Error("an unknown field accepted")
	}
	if _, err := ParseHost([]byte(`{}`)); err == nil {
		t.Error("an empty document accepted")
	}
}

func TestSetMovesForwardOnlyAndNeverRewritesARelease(t *testing.T) {
	h := hostIndex(t, "host-3")
	if h.Sequence != 2 {
		t.Errorf("sequence after Set = %d, want 2 (it starts at 1 and every Set raises it)", h.Sequence)
	}
	if err := h.Set("host-2", hostHashes); err == nil {
		t.Error("an older release was accepted")
	}
	if err := h.Set("host-3", map[string]string{"amd64": hashC, "arm64": hashD}); err == nil {
		t.Error("a published release was replaced with other binaries")
	}
	if h.Version != "host-3" || h.Binaries[0].SHA256 != hashA {
		t.Errorf("a refused Set changed the document: %+v", h)
	}
	if err := h.Set("host-3", hostHashes); err != nil {
		t.Errorf("republishing the identical release must work: %v", err)
	}
	if err := h.Set("host-10", map[string]string{"amd64": hashC, "arm64": hashD}); err != nil {
		t.Errorf("host-10 after host-3 (numeric, not text order): %v", err)
	}
	if h.Sequence != 4 {
		t.Errorf("sequence = %d, want 4", h.Sequence)
	}
	if err := h.Set("host-11", map[string]string{"amd64": hashC}); err == nil {
		t.Error("a release missing an architecture was accepted")
	}
	if err := h.Set("host-12", map[string]string{"amd64": hashC, "arm64": hashD, "riscv64": hashA}); err == nil {
		t.Error("a release with a stray architecture was accepted")
	}
}

func TestHostNewerComparesNumbers(t *testing.T) {
	cases := []struct {
		candidate, installed string
		want                 bool
	}{
		{"host-10", "host-9", true},
		{"host-9", "host-10", false},
		{"host-4", "host-4", false},
		{"host-1", "dev", true},
		{"host-1", "", true},
		{"dev", "host-1", false},
		{"v3.10.2-lucx.307", "host-1", false},
	}
	for _, c := range cases {
		if got := HostNewer(c.candidate, c.installed); got != c.want {
			t.Errorf("HostNewer(%q, %q) = %v, want %v", c.candidate, c.installed, got, c.want)
		}
	}
}

func TestHostAndBuildsSignaturesAreNotInterchangeable(t *testing.T) {
	pub, priv := keypair(t)
	keys := []ed25519.PublicKey{pub}
	doc := []byte(`{"schema":1}` + "\n")

	if err := VerifyHost(doc, SignHost(priv, doc), keys); err != nil {
		t.Fatalf("a host signature does not verify as one: %v", err)
	}
	if err := Verify(doc, SignHost(priv, doc), keys); err == nil {
		t.Error("a host signature was accepted for the builds list")
	}
	if err := VerifyHost(doc, Sign(priv, doc), keys); err == nil {
		t.Error("a builds signature was accepted for the host document")
	}
	if err := VerifyHost([]byte("{}\n"), SignHost(priv, doc), keys); err == nil {
		t.Error("a changed host document verified")
	}
}
