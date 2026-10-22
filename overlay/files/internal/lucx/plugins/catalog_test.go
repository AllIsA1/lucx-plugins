// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func newCatalogKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func goodCatalogEntry(id string) map[string]any {
	return map[string]any{
		"id": id, "name": "Plugin " + id, "summary": "Does a thing", "repo": "https://github.com/acme/" + id,
		"commit": testCommit, "version": "1.2.0", "manifestSha256": strings.Repeat("ab", 32),
		"type": "process", "scope": "both", "permissions": []string{"api:clients:read", "network"},
		"icon": "filter", "license": "MIT", "author": "acme",
		"verified": map[string]any{"by": "LucX", "at": "2026-10-10", "notes": "reviewed"},
	}
}

func catalogJSON(t *testing.T, mutate func(idx map[string]any)) []byte {
	t.Helper()
	idx := map[string]any{
		"schema": 1, "sequence": 7, "generatedAt": "2026-10-19T12:00:00Z",
		"plugins": []any{goodCatalogEntry("block-ads"), goodCatalogEntry("traffic-alerts")},
	}
	if mutate != nil {
		mutate(idx)
	}
	raw, err := json.Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestCatalogSignatureAcceptsAnyKnownKeyAndNothingElse(t *testing.T) {
	pubA, privA := newCatalogKey(t)
	pubB, _ := newCatalogKey(t)
	index := catalogJSON(t, nil)
	sig := SignCatalog(privA, index)

	if err := VerifyCatalog(index, sig, []ed25519.PublicKey{pubB, pubA}); err != nil {
		t.Errorf("the second key of the list signed it: %v", err)
	}
	if err := VerifyCatalog(index, sig, []ed25519.PublicKey{pubB}); !errors.Is(err, ErrCatalogSignature) {
		t.Errorf("a key that did not sign: %v, want ErrCatalogSignature", err)
	}
	if err := VerifyCatalog(index, sig, nil); !errors.Is(err, ErrCatalogNoKeys) {
		t.Errorf("no keys: %v, want ErrCatalogNoKeys", err)
	}
}

func TestCatalogSignatureCoversEveryByteAndIsBoundToTheCatalog(t *testing.T) {
	pub, priv := newCatalogKey(t)
	keys := []ed25519.PublicKey{pub}
	index := catalogJSON(t, nil)
	sig := SignCatalog(priv, index)

	for i := range index {
		tampered := append([]byte(nil), index...)
		tampered[i] ^= 0x01
		if err := VerifyCatalog(tampered, sig, keys); err == nil {
			t.Fatalf("flipping byte %d of the index went unnoticed", i)
		}
	}
	for name, bad := range map[string]string{
		"empty": "", "not base64": "!!!", "too short": base64.StdEncoding.EncodeToString([]byte("short")),
		"padding spelling": strings.TrimRight(sig, "=") + "=",
	} {
		if bad == sig {
			continue
		}
		if err := VerifyCatalog(index, bad, keys); err == nil {
			t.Errorf("%s signature accepted", name)
		}
	}
	// A signature over the raw bytes, without the catalog's domain prefix, is not a catalog signature.
	plain := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, index))
	if err := VerifyCatalog(index, plain, keys); err == nil {
		t.Error("a signature made without the catalog domain separation was accepted")
	}
	// base64 padding bits: a different spelling of the same signature must not verify.
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	body := strings.TrimRight(sig, "=")
	last := strings.IndexByte(alphabet, body[len(body)-1])
	if err := VerifyCatalog(index, body[:len(body)-1]+string(alphabet[last^1])+strings.Repeat("=", len(sig)-len(body)), keys); err == nil {
		t.Error("a malleable signature spelling was accepted")
	}
	if err := VerifyCatalog(index, " "+sig+"\n", keys); err != nil {
		t.Errorf("surrounding whitespace in the .sig file must not matter: %v", err)
	}
}

func TestParseCatalogKeys(t *testing.T) {
	pub, _ := newCatalogKey(t)
	enc := base64.StdEncoding.EncodeToString(pub)
	keys, err := ParseCatalogKeys([]string{enc, " " + enc + " "})
	if err != nil || len(keys) != 2 || !keys[0].Equal(pub) {
		t.Fatalf("keys = %v, %v", keys, err)
	}
	for _, bad := range []string{"", "!!!", base64.StdEncoding.EncodeToString([]byte("too short"))} {
		if _, err := ParseCatalogKeys([]string{bad}); err == nil {
			t.Errorf("key %q accepted", bad)
		}
	}
}

func TestParseCatalogIndexReadsAGoodCatalog(t *testing.T) {
	idx, err := ParseCatalogIndex(catalogJSON(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if idx.Sequence != 7 || len(idx.Plugins) != 2 || idx.Plugins[0].ID != "block-ads" {
		t.Fatalf("index = %+v", idx)
	}
	e := idx.Plugins[0]
	if e.Source.Owner != "acme" || e.Source.Repo != "block-ads" || e.Source.Ref != "" || e.Commit != testCommit || e.Verified == nil || e.Verified.By != "LucX" {
		t.Errorf("entry = %+v", e)
	}
}

func TestParseCatalogIndexRefusesWhatCouldMislead(t *testing.T) {
	setEntry := func(field string, value any) func(map[string]any) {
		return func(idx map[string]any) {
			e := goodCatalogEntry("block-ads")
			e[field] = value
			idx["plugins"] = []any{e}
		}
	}
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"newer schema", func(i map[string]any) { i["schema"] = 2 }},
		{"no sequence", func(i map[string]any) { i["sequence"] = 0 }},
		{"negative sequence", func(i map[string]any) { i["sequence"] = -3 }},
		{"bad date", func(i map[string]any) { i["generatedAt"] = "yesterday" }},
		{"duplicate id", func(i map[string]any) { i["plugins"] = []any{goodCatalogEntry("same-id"), goodCatalogEntry("same-id")} }},
		{"too many plugins", func(i map[string]any) {
			var list []any
			for n := 0; n < MaxCatalogPlugins+1; n++ {
				list = append(list, goodCatalogEntry("p"+strings.Repeat("a", 3)+string(rune('a'+n%26))+string(rune('a'+n/26%26))+string(rune('a'+n/676))))
			}
			i["plugins"] = list
		}},
		{"hostile id", setEntry("id", "../etc")},
		{"empty name", setEntry("name", "")},
		{"long name", setEntry("name", strings.Repeat("x", 61))},
		{"long summary", setEntry("summary", strings.Repeat("x", 301))},
		{"repo with a ref", setEntry("repo", "https://github.com/acme/x/tree/main")},
		{"repo on another host", setEntry("repo", "https://evil.example/acme/x")},
		{"branch instead of commit", setEntry("commit", "main")},
		{"short commit", setEntry("commit", "0123456")},
		{"upper-case commit", setEntry("commit", strings.ToUpper(testCommit))},
		{"bad manifest hash", setEntry("manifestSha256", "abc")},
		{"upper-case manifest hash", setEntry("manifestSha256", strings.Repeat("AB", 32))},
		{"bad version", setEntry("version", "1.2")},
		{"unknown permission", setEntry("permissions", []string{"root"})},
		{"unknown event permission", setEntry("permissions", []string{"events:no.such"})},
		{"bad scope", setEntry("scope", "everywhere")},
		{"bad type", setEntry("type", "binary")},
		{"bad icon", setEntry("icon", "Bad Icon!")},
		{"long license", setEntry("license", strings.Repeat("x", 41))},
		{"long verifier", setEntry("verified", map[string]any{"by": strings.Repeat("x", 41), "at": "2026-10-10"})},
		{"verified without a date", setEntry("verified", map[string]any{"by": "LucX", "at": ""})},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseCatalogIndex(catalogJSON(t, tc.mutate)); err == nil {
				t.Error("accepted")
			}
		})
	}
	if _, err := ParseCatalogIndex(append(catalogJSON(t, nil), []byte(` {"x":1}`)...)); err == nil {
		t.Error("data after the JSON document accepted")
	}
	if _, err := ParseCatalogIndex([]byte(strings.Repeat(" ", MaxCatalogBytes+1))); err == nil {
		t.Error("an oversized catalog accepted")
	}
	if _, err := ParseCatalogIndex([]byte(`not json`)); err == nil {
		t.Error("garbage accepted")
	}
}

func TestParseCatalogIndexAllowsUnknownFieldsAndAnEmptyCatalog(t *testing.T) {
	raw := catalogJSON(t, func(i map[string]any) {
		i["note"] = "from a newer index tool"
		i["plugins"] = []any{}
	})
	if idx, err := ParseCatalogIndex(raw); err != nil || len(idx.Plugins) != 0 {
		t.Errorf("empty catalog = %+v, %v", idx, err)
	}
	raw = catalogJSON(t, func(i map[string]any) {
		e := goodCatalogEntry("block-ads")
		e["screenshots"] = []string{"https://example.com/a.png"}
		i["plugins"] = []any{e}
	})
	if _, err := ParseCatalogIndex(raw); err != nil {
		t.Errorf("an extra field made the entry unreadable: %v", err)
	}
}

func TestDefaultCatalogKeysTakeTheEnvironmentAndSkipBadOnes(t *testing.T) {
	pubA, _ := newCatalogKey(t)
	pubB, _ := newCatalogKey(t)
	encA, encB := base64.StdEncoding.EncodeToString(pubA), base64.StdEncoding.EncodeToString(pubB)
	t.Setenv("XUI_PLUGINS_CATALOG_KEYS", encA+", not-a-key ,"+encB+",")
	keys := DefaultCatalogKeys()
	n := len(builtinCatalogKeys)
	if len(keys) != n+2 || !keys[n].Equal(pubA) || !keys[n+1].Equal(pubB) {
		t.Errorf("keys = %d, want the built-in ones, then the two good ones from the environment in order", len(keys))
	}
	t.Setenv("XUI_PLUGINS_CATALOG_KEYS", "")
	if got := DefaultCatalogKeys(); len(got) != len(builtinCatalogKeys) {
		t.Errorf("without the variable: %d keys, want only the built-in ones", len(got))
	}
}

func TestBuiltinCatalogKeysAreTheProjectsCurrentAndSpareKey(t *testing.T) {
	keys, err := ParseCatalogKeys(builtinCatalogKeys)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0].Equal(keys[1]) {
		t.Fatalf("%d built-in keys, want a current and a different spare", len(keys))
	}
	t.Setenv("XUI_PLUGINS_CATALOG_KEYS", "")
	if got := DefaultCatalogKeys(); len(got) != 2 || !got[0].Equal(keys[0]) || !got[1].Equal(keys[1]) {
		t.Error("the default keys must be the built-in ones, current first")
	}
}
