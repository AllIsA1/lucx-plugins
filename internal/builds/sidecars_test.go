// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package builds

import (
	"crypto/ed25519"
	"strings"
	"testing"
)

const testCommit40 = "4f9c786cdd1d9fcd0c133e2e0fec0c5a12522610"

var sidecarHashes = map[string]string{"amd64": hashA, "arm64": hashB}

func sidecarIndex(t *testing.T) *SidecarIndex {
	t.Helper()
	i := &SidecarIndex{Schema: SidecarSchemaVersion, Sequence: 1}
	if err := i.Set("openflux", "v0.13.0", testCommit40, "sidecar-openflux-v0.13.0-1", sidecarHashes); err != nil {
		t.Fatal(err)
	}
	return i
}

func TestSidecarDocumentRoundTripsAndNamesItsDownloads(t *testing.T) {
	raw, err := MarshalSidecars(sidecarIndex(t))
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseSidecars(raw)
	if err != nil {
		t.Fatalf("our own document does not parse: %v", err)
	}
	again, _ := MarshalSidecars(back)
	if string(raw) != string(again) {
		t.Error("marshal, parse, marshal is not stable")
	}
	s, ok := back.Find("openflux", "arm64")
	want := "https://github.com/AllIsA1/lucx-plugins/releases/download/sidecar-openflux-v0.13.0-1/openflux-linux-arm64"
	if !ok || s.URL != want || s.SHA256 != hashB || s.Commit != testCommit40 {
		t.Errorf("arm64 = %+v", s)
	}
	if _, ok := back.Find("openflux", "riscv64"); ok {
		t.Error("found a binary for an architecture nobody built")
	}
}

func TestSidecarValidateRejectsWhatTheClientMustNotTrust(t *testing.T) {
	cases := map[string]func(i *SidecarIndex){
		"wrong schema":         func(i *SidecarIndex) { i.Schema = 2 },
		"zero sequence":        func(i *SidecarIndex) { i.Sequence = 0 },
		"unknown program":      func(i *SidecarIndex) { i.Sidecars[0].Name = "rm" },
		"short commit":         func(i *SidecarIndex) { i.Sidecars[0].Commit = "v0.13.0" },
		"uppercase hash":       func(i *SidecarIndex) { i.Sidecars[0].SHA256 = strings.Repeat("A", 64) },
		"same hash twice":      func(i *SidecarIndex) { i.Sidecars[1].SHA256 = i.Sidecars[0].SHA256 },
		"foreign download url": func(i *SidecarIndex) { i.Sidecars[0].URL = "https://evil.example/openflux" },
		"release of another version": func(i *SidecarIndex) {
			i.Sidecars[0].Release = "sidecar-openflux-v0.12.0-1"
		},
		"one architecture":         func(i *SidecarIndex) { i.Sidecars = i.Sidecars[:1] },
		"arches from two releases": func(i *SidecarIndex) { i.Sidecars[1].Commit = strings.Repeat("a", 40) },
		"listed twice":             func(i *SidecarIndex) { i.Sidecars = append(i.Sidecars, i.Sidecars[0]) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			i := sidecarIndex(t)
			mutate(i)
			if err := i.Validate(); err == nil {
				t.Error("accepted")
			}
		})
	}
}

func TestSidecarSetKeepsReleasesImmutableAndNeverGoesBackwards(t *testing.T) {
	i := sidecarIndex(t)
	before := i.Sequence
	if err := i.Set("openflux", "v0.13.0", testCommit40, "sidecar-openflux-v0.13.0-1", sidecarHashes); err != nil || i.Sequence != before+1 {
		t.Fatalf("the same release with the same hashes: %v (sequence %d, was %d)", err, i.Sequence, before)
	}
	other := map[string]string{"amd64": hashB, "arm64": hashA}
	if err := i.Set("openflux", "v0.13.0", testCommit40, "sidecar-openflux-v0.13.0-1", other); err == nil {
		t.Error("a release was changed")
	}
	if err := i.Set("openflux", "v0.12.9", testCommit40, "sidecar-openflux-v0.12.9-1", other); err == nil {
		t.Error("an older upstream version was accepted")
	}
	if err := i.Set("openflux", "v0.13.1", testCommit40, "sidecar-openflux-v0.13.1-1", other); err != nil {
		t.Errorf("a newer version: %v", err)
	}
	if s, _ := i.Find("openflux", "amd64"); s.Version != "v0.13.1" {
		t.Errorf("amd64 = %+v", s)
	}
}

func TestSidecarSignatureIsBoundToSidecars(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	raw, _ := MarshalSidecars(sidecarIndex(t))
	sig := SignSidecars(priv, raw)
	if err := VerifySidecars(raw, sig, []ed25519.PublicKey{pub}); err != nil {
		t.Fatal(err)
	}
	if err := VerifySidecars(append([]byte(" "), raw...), sig, []ed25519.PublicKey{pub}); err == nil {
		t.Error("a changed document verified")
	}
	if err := Verify(raw, sig, []ed25519.PublicKey{pub}); err == nil {
		t.Error("a sidecar signature is valid for the builds list")
	}
	if err := VerifyHost(raw, sig, []ed25519.PublicKey{pub}); err == nil {
		t.Error("a sidecar signature is valid for the host document")
	}
}
