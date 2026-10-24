// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AllIsA1/lucx-plugins/internal/plugins"
)

const (
	sha1c = "0123456789abcdef0123456789abcdef01234567"
	sha2c = "2222222222222222222222222222222222222222"
)

func run3(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func writePlugin(t *testing.T, mutate func(m map[string]any)) (dir, manifest string) {
	t.Helper()
	m := map[string]any{
		"id": "block-ads", "name": "Block ads", "version": "1.2.0", "apiVersion": "1", "license": "MIT", "author": "acme",
		"type": "process", "scope": "both", "run": map[string]any{"command": "bin/${arch}/blockads"},
		"permissions": []string{"pipeline:xray.config"},
		"ui":          map[string]any{"menu": []map[string]any{}},
	}
	if mutate != nil {
		mutate(m)
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lucx-plugin.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, string(raw)
}

func TestKeygenWritesAPrivateKeyOnceAndPrintsThePublicOne(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "catalog.key")
	code, out, errOut := run3(t, "keygen", "-out", key)
	if code != 0 {
		t.Fatalf("keygen = %d %s", code, errOut)
	}
	st, err := os.Stat(key)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("key file = %v, %v; want mode 0600", st, err)
	}
	pub := strings.TrimSpace(out)
	if raw, err := base64.StdEncoding.DecodeString(pub); err != nil || len(raw) != ed25519.PublicKeySize {
		t.Fatalf("printed public key %q", out)
	}
	if code, out2, _ := run3(t, "pubkey", "-key", key); code != 0 || strings.TrimSpace(out2) != pub {
		t.Errorf("pubkey = %d %q, want %q", code, out2, pub)
	}
	before, _ := os.ReadFile(key)
	if code, _, _ := run3(t, "keygen", "-out", key); code == 0 {
		t.Error("keygen overwrote an existing key")
	}
	if after, _ := os.ReadFile(key); !bytes.Equal(before, after) {
		t.Error("the existing key changed")
	}
}

func TestAddFillsAnEntryFromThePluginAndCountsTheSequence(t *testing.T) {
	plugin, manifest := writePlugin(t, nil)
	index := filepath.Join(t.TempDir(), "index.json")
	args := []string{"add", "-index", index, "-repo", "https://github.com/acme/block-ads", "-commit", sha1c, "-dir", plugin, "-summary", "Blocks ads", "-verified-by", "LucX", "-verified-at", "2026-10-10"}
	if code, _, errOut := run3(t, args...); code != 0 {
		t.Fatalf("add = %d %s", code, errOut)
	}
	raw, _ := os.ReadFile(index)
	idx, err := plugins.ParseCatalogIndex(raw)
	if err != nil || len(idx.Plugins) != 1 || idx.Sequence != 1 {
		t.Fatalf("index = %+v, %v", idx, err)
	}
	sum := sha256.Sum256([]byte(manifest))
	e := idx.Plugins[0]
	if e.ID != "block-ads" || e.Commit != sha1c || e.ManifestSHA256 != hex.EncodeToString(sum[:]) || e.Scope != "both" ||
		e.Summary != "Blocks ads" || e.Verified == nil || e.Verified.By != "LucX" || len(e.Permissions) != 1 {
		t.Errorf("entry = %+v", e)
	}

	// A new commit of the same plugin replaces its entry and moves the sequence on.
	args[6] = sha2c
	if code, _, errOut := run3(t, args...); code != 0 {
		t.Fatalf("second add = %d %s", code, errOut)
	}
	raw, _ = os.ReadFile(index)
	idx, _ = plugins.ParseCatalogIndex(raw)
	if len(idx.Plugins) != 1 || idx.Plugins[0].Commit != sha2c || idx.Sequence != 2 {
		t.Errorf("after the second add: %+v", idx)
	}

	other, _ := writePlugin(t, func(m map[string]any) { m["id"], m["name"] = "alpha-plugin", "Alpha" })
	if code, _, errOut := run3(t, "add", "-index", index, "-repo", "https://github.com/acme/alpha", "-commit", sha1c, "-dir", other, "-verified-by", "LucX", "-verified-at", "2026-10-10"); code != 0 {
		t.Fatalf("third add = %d %s", code, errOut)
	}
	raw, _ = os.ReadFile(index)
	idx, _ = plugins.ParseCatalogIndex(raw)
	if len(idx.Plugins) != 2 || idx.Plugins[0].ID != "alpha-plugin" || idx.Plugins[1].ID != "block-ads" {
		t.Errorf("plugins must be listed in id order: %+v", idx.Plugins)
	}
}

func TestAddRefusesWhatTheCatalogWouldRefuse(t *testing.T) {
	plugin, _ := writePlugin(t, nil)
	badManifest, _ := writePlugin(t, func(m map[string]any) { m["version"] = "one" })
	index := filepath.Join(t.TempDir(), "index.json")
	base := []string{"add", "-index", index, "-repo", "https://github.com/acme/block-ads", "-commit", sha1c, "-dir", plugin}
	with := func(i int, v string) []string {
		out := append([]string(nil), base...)
		out[i] = v
		return out
	}
	for name, args := range map[string][]string{
		"a branch instead of a commit": with(6, "main"),
		"a repo with a ref":            with(4, "https://github.com/acme/block-ads/tree/main"),
		"an invalid manifest":          with(8, badManifest),
		"a missing directory":          with(8, filepath.Join(plugin, "nope")),
	} {
		if code, _, _ := run3(t, args...); code == 0 {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := os.Stat(index); err == nil {
		t.Error("a refused add still wrote an index")
	}
}

func TestSignThenVerify(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "catalog.key")
	_, pub, _ := run3(t, "keygen", "-out", key)
	plugin, _ := writePlugin(t, nil)
	index := filepath.Join(dir, "index.json")
	if code, _, errOut := run3(t, "add", "-index", index, "-repo", "https://github.com/acme/block-ads", "-commit", sha1c, "-dir", plugin, "-verified-by", "LucX", "-verified-at", "2026-10-10"); code != 0 {
		t.Fatal(errOut)
	}
	if code, _, errOut := run3(t, "sign", "-key", key, "-index", index); code != 0 {
		t.Fatalf("sign = %d %s", code, errOut)
	}
	sig := index + ".sig"
	rawIdx, _ := os.ReadFile(index)
	rawSig, _ := os.ReadFile(sig)
	pubKeys, _ := plugins.ParseCatalogKeys([]string{strings.TrimSpace(pub)})
	if err := plugins.VerifyCatalog(rawIdx, string(rawSig), pubKeys); err != nil {
		t.Fatalf("the signature the tool wrote does not verify with the panel's code: %v", err)
	}
	if code, _, _ := run3(t, "verify", "-index", index, "-sig", sig, "-pub", strings.TrimSpace(pub)); code != 0 {
		t.Error("verify rejected a good signature")
	}
	if err := os.WriteFile(index, append(rawIdx, ' '), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := run3(t, "verify", "-index", index, "-sig", sig, "-pub", strings.TrimSpace(pub)); code == 0 {
		t.Error("verify accepted an edited index")
	}
}

func TestSignRefusesAnIndexThePanelWouldRefuse(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "catalog.key")
	run3(t, "keygen", "-out", key)
	index := filepath.Join(dir, "index.json")
	if err := os.WriteFile(index, []byte(`{"schema":1,"sequence":0,"generatedAt":"2026-10-19T12:00:00Z","plugins":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := run3(t, "sign", "-key", key, "-index", index); code == 0 {
		t.Error("an index with no sequence was signed")
	}
	if _, err := os.Stat(index + ".sig"); err == nil {
		t.Error("a signature was written for an invalid index")
	}
}

func TestAddTrackWritesAnEntryWithoutACommit(t *testing.T) {
	plugin, _ := writePlugin(t, nil)
	index := filepath.Join(t.TempDir(), "index.json")
	if code, _, errOut := run3(t, "add", "-index", index, "-repo", "https://github.com/acme/block-ads", "-track", "-dir", plugin); code != 0 {
		t.Fatalf("add -track = %d %s", code, errOut)
	}
	raw, _ := os.ReadFile(index)
	idx, err := plugins.ParseCatalogIndex(raw)
	if err != nil || len(idx.Plugins) != 1 || !idx.Plugins[0].Track || idx.Plugins[0].Commit != "" || idx.Plugins[0].ManifestSHA256 != "" {
		t.Fatalf("index = %+v, %v", idx, err)
	}
	// With a commit the entry still pins it for panels that do not know -track.
	if code, _, errOut := run3(t, "add", "-index", index, "-repo", "https://github.com/acme/block-ads", "-track", "-commit", sha1c, "-dir", plugin); code != 0 {
		t.Fatalf("add -track -commit = %d %s", code, errOut)
	}
	raw, _ = os.ReadFile(index)
	idx, err = plugins.ParseCatalogIndex(raw)
	if err != nil || !idx.Plugins[0].Track || idx.Plugins[0].Commit != sha1c || idx.Plugins[0].ManifestSHA256 == "" {
		t.Fatalf("index = %+v, %v", idx, err)
	}
}

func TestRemoveTakesAPluginOutAndMovesTheSequenceOn(t *testing.T) {
	plugin, _ := writePlugin(t, nil)
	other, _ := writePlugin(t, func(m map[string]any) { m["id"], m["name"] = "alpha-plugin", "Alpha" })
	index := filepath.Join(t.TempDir(), "index.json")
	for repo, dir := range map[string]string{"block-ads": plugin, "alpha": other} {
		if code, _, errOut := run3(t, "add", "-index", index, "-repo", "https://github.com/acme/"+repo, "-commit", sha1c, "-dir", dir); code != 0 {
			t.Fatalf("add %s = %d %s", repo, code, errOut)
		}
	}
	raw, _ := os.ReadFile(index)
	before, _ := plugins.ParseCatalogIndex(raw)
	if code, _, errOut := run3(t, "remove", "-index", index, "-id", "block-ads"); code != 0 {
		t.Fatalf("remove = %d %s", code, errOut)
	}
	raw, _ = os.ReadFile(index)
	after, err := plugins.ParseCatalogIndex(raw)
	if err != nil || len(after.Plugins) != 1 || after.Plugins[0].ID != "alpha-plugin" || after.Sequence != before.Sequence+1 {
		t.Fatalf("after = %+v, %v", after, err)
	}
	if code, _, _ := run3(t, "remove", "-index", index, "-id", "block-ads"); code == 0 {
		t.Error("removing a plugin that is not there succeeded")
	}
	raw2, _ := os.ReadFile(index)
	if string(raw2) != string(raw) {
		t.Error("a refused removal changed the index")
	}
	if code, _, _ := run3(t, "remove", "-index", index); code == 0 {
		t.Error("remove without -id succeeded")
	}
}
