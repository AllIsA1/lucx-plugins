// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package builds

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

const (
	hashA = "1111111111111111111111111111111111111111111111111111111111111111"
	hashB = "2222222222222222222222222222222222222222222222222222222222222222"
	hashC = "3333333333333333333333333333333333333333333333333333333333333333"
	hashD = "4444444444444444444444444444444444444444444444444444444444444444"
)

func sample(tag, arch string) Build {
	release := tag + "-plugins.1"
	original, ours := hashA, hashD
	if arch == "arm64" {
		original, ours = hashB, hashC
	}
	return Build{
		Upstream:             tag,
		Arch:                 arch,
		OriginalBinarySha256: original,
		OriginalArchive: Archive{
			URL:    "https://github.com/AlexeyLCP/lucx-ui/releases/download/" + tag + "/x-ui-linux-" + arch + ".tar.gz",
			SHA256: hashB,
		},
		Release: release,
		Build: Archive{
			URL:    "https://github.com/AllIsA1/lucx-plugins/releases/download/" + release + "/x-ui-plugins-linux-" + arch + ".tar.gz",
			SHA256: hashC,
		},
		BinarySha256: ours,
	}
}

func index(builds ...Build) *Index {
	return &Index{Schema: SchemaVersion, Sequence: 1, GeneratedAt: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC), Builds: builds}
}

func TestMarshalParseRoundTripIsStable(t *testing.T) {
	idx := index(sample("v3.10.2-lucx.307", "amd64"), sample("v3.10.2-lucx.307", "arm64"))
	raw, err := Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(raw, []byte("}\n")) {
		t.Errorf("document must end with a newline: %q", raw[len(raw)-5:])
	}
	back, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse(Marshal(x)): %v", err)
	}
	again, err := Marshal(back)
	if err != nil || !bytes.Equal(raw, again) {
		t.Errorf("the document changed on a round trip: %v", err)
	}
}

func TestValidateRejectsWhatTheClientMustNotTrust(t *testing.T) {
	cases := map[string]struct {
		mutate func(*Index)
		want   string
	}{
		"schema":         {func(i *Index) { i.Schema = 2 }, "schema"},
		"sequence":       {func(i *Index) { i.Sequence = 0 }, "sequence"},
		"tag":            {func(i *Index) { i.Builds[0].Upstream = "latest" }, "upstream tag"},
		"arch":           {func(i *Index) { i.Builds[0].Arch = "armv7" }, "arch"},
		"original hash":  {func(i *Index) { i.Builds[0].OriginalBinarySha256 = "ABC" }, "originalBinarySha256"},
		"uppercase hash": {func(i *Index) { i.Builds[0].BinarySha256 = strings.Repeat("A", 64) }, "binarySha256"},
		"same binary":    {func(i *Index) { i.Builds[0].BinarySha256 = i.Builds[0].OriginalBinarySha256 }, "must differ"},
		"original from elsewhere": {func(i *Index) {
			i.Builds[0].OriginalArchive.URL = "https://evil.example/x-ui-linux-amd64.tar.gz"
		}, "original archive"},
		"original for another tag": {func(i *Index) {
			i.Builds[0].OriginalArchive.URL = strings.Replace(i.Builds[0].OriginalArchive.URL, "lucx.307", "lucx.1", 1)
		}, "original archive"},
		"build from elsewhere": {func(i *Index) {
			i.Builds[0].Build.URL = "https://github.com/AlexeyLCP/lucx-ui/releases/download/x/y.tar.gz"
		}, "build archive"},
		"http":           {func(i *Index) { i.Builds[0].Build.URL = strings.Replace(i.Builds[0].Build.URL, "https", "http", 1) }, "build archive"},
		"release name":   {func(i *Index) { i.Builds[0].Release = "other" }, "release"},
		"archive hash":   {func(i *Index) { i.Builds[0].Build.SHA256 = "zz" }, "build archive sha256"},
		"duplicate":      {func(i *Index) { i.Builds = append(i.Builds, i.Builds[0]) }, "duplicate"},
		"unsorted":       {func(i *Index) { i.Builds[0], i.Builds[1] = i.Builds[1], i.Builds[0] }, "sorted"},
		"one hash twice": {func(i *Index) { i.Builds[1].OriginalBinarySha256 = i.Builds[0].OriginalBinarySha256 }, "same original"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			idx := index(sample("v3.10.2-lucx.307", "amd64"), sample("v3.10.2-lucx.307", "arm64"))
			if err := idx.Validate(); err != nil {
				t.Fatalf("the base document must be valid: %v", err)
			}
			c.mutate(idx)
			err := idx.Validate()
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Validate = %v, want an error mentioning %q", err, c.want)
			}
		})
	}
}

func TestParseIsStrict(t *testing.T) {
	good, _ := Marshal(index(sample("v3.10.2-lucx.307", "amd64")))
	for name, doc := range map[string]string{
		"unknown field": strings.Replace(string(good), `"schema": 1`, `"schema": 1, "extra": true`, 1),
		"trailing data": string(good) + `{}`,
		"not json":      "nope",
	} {
		if _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("%s: Parse accepted the document", name)
		}
	}
}

func TestAddReplacesTheSameBuildAndKeepsTheListSorted(t *testing.T) {
	idx := index()
	idx.Sequence = 4
	b307 := sample("v3.10.2-lucx.307", "arm64")
	b308 := sample("v3.10.2-lucx.308", "amd64")
	b307amd := sample("v3.10.2-lucx.307", "amd64")
	for _, b := range []Build{b308, b307, b307amd} {
		if err := idx.Add(b); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	order := ""
	for _, b := range idx.Builds {
		order += b.Upstream[len(b.Upstream)-3:] + b.Arch + " "
	}
	if order != "307amd64 307arm64 308amd64 " {
		t.Errorf("order = %q", order)
	}
	if idx.Sequence != 7 {
		t.Errorf("sequence = %d, want it raised by one per addition", idx.Sequence)
	}
	rebuilt := sample("v3.10.2-lucx.307", "amd64")
	rebuilt.Release, rebuilt.BinarySha256 = "v3.10.2-lucx.307-plugins.2", hashA[:63]+"9"
	rebuilt.Build.URL = "https://github.com/AllIsA1/lucx-plugins/releases/download/v3.10.2-lucx.307-plugins.2/x-ui-plugins-linux-amd64.tar.gz"
	if err := idx.Add(rebuilt); err != nil {
		t.Fatal(err)
	}
	if len(idx.Builds) != 3 || idx.Builds[0].Release != "v3.10.2-lucx.307-plugins.2" {
		t.Errorf("a rebuild must replace the earlier entry: %+v", idx.Builds[0])
	}
}

func TestLookupFindsTheOriginalOrOurOwnBuild(t *testing.T) {
	idx := index(sample("v3.10.2-lucx.307", "amd64"))
	if b, kind := idx.Lookup(hashA, "amd64"); b == nil || kind != Original {
		t.Errorf("original binary: %v %v", b, kind)
	}
	if b, kind := idx.Lookup(hashD, "amd64"); b == nil || kind != Ours {
		t.Errorf("our own build must be recognised so it is never swapped again: %v %v", b, kind)
	}
	if b, _ := idx.Lookup(hashA, "arm64"); b != nil {
		t.Error("a hash only counts for the architecture it was built for")
	}
	if b, kind := idx.Lookup(hashC, "amd64"); b != nil || kind != Unknown {
		t.Errorf("an unknown binary must not match: %v %v", b, kind)
	}
}

func keypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func TestSignAndVerify(t *testing.T) {
	pub, priv := keypair(t)
	spare, _ := keypair(t)
	data := []byte("{}\n")
	sig := Sign(priv, data)
	if err := Verify(data, sig, []ed25519.PublicKey{pub}); err != nil {
		t.Fatalf("valid signature refused: %v", err)
	}
	if err := Verify(data, sig, []ed25519.PublicKey{spare, pub}); err != nil {
		t.Errorf("any key of the list may have signed: %v", err)
	}
	if err := Verify([]byte("{} \n"), sig, []ed25519.PublicKey{pub}); err == nil {
		t.Error("a changed document verified")
	}
	if err := Verify(data, sig, []ed25519.PublicKey{spare}); err == nil {
		t.Error("a foreign key verified")
	}
	if err := Verify(data, sig, nil); err == nil {
		t.Error("no keys must not verify anything")
	}
	if err := Verify(data, sig[:len(sig)-4], []ed25519.PublicKey{pub}); err == nil {
		t.Error("a truncated signature verified")
	}
	raw, _ := base64.StdEncoding.DecodeString(sig)
	sloppy := base64.RawStdEncoding.EncodeToString(raw)
	if err := Verify(data, sloppy, []ed25519.PublicKey{pub}); err == nil {
		t.Error("non-canonical base64 verified")
	}
}

func TestSignatureIsBoundToThisPurpose(t *testing.T) {
	pub, priv := keypair(t)
	data := []byte(`{"schema":1}` + "\n")
	for name, prefix := range map[string]string{"bare": "", "plugin catalog": "lucx-plugins-index-v1\n"} {
		sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, append([]byte(prefix), data...)))
		if err := Verify(data, sig, []ed25519.PublicKey{pub}); err == nil {
			t.Errorf("a %s signature was accepted as a builds signature", name)
		}
	}
}

func TestParseKeysIsStrict(t *testing.T) {
	pub, _ := keypair(t)
	good := base64.StdEncoding.EncodeToString(pub)
	if keys, err := ParseKeys([]string{good, " " + good + "\n"}); err != nil || len(keys) != 2 {
		t.Errorf("ParseKeys(good) = %v, %v", keys, err)
	}
	for _, bad := range []string{"", "AAAA", good[:len(good)-2], good + "AAAA"} {
		if _, err := ParseKeys([]string{bad}); err == nil {
			t.Errorf("ParseKeys accepted %q", bad)
		}
	}
}
