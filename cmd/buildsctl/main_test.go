// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AllIsA1/lucx-plugins/internal/builds"
)

func exec(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut)
	return strings.TrimSpace(out.String()), strings.TrimSpace(errOut.String()), code
}

func must(t *testing.T, args ...string) string {
	t.Helper()
	out, errOut, code := exec(t, args...)
	if code != 0 {
		t.Fatalf("buildsctl %s: exit %d: %s", strings.Join(args, " "), code, errOut)
	}
	return out
}

func describe(t *testing.T, dir, tag, arch, original, ours string) string {
	t.Helper()
	path := filepath.Join(dir, tag+"-"+arch+".json")
	must(t, "meta", "-upstream", tag, "-arch", arch, "-release", tag+"-plugins.1",
		"-original-binary-sha256", original, "-original-archive-sha256", strings.Repeat("a", 64),
		"-build-archive-sha256", strings.Repeat("b", 64), "-binary-sha256", ours, "-out", path)
	return path
}

func TestKeygenWritesAPrivateKeyOnceAndPrintsThePublicOne(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "builds.key")
	pub := must(t, "keygen", "-out", key)
	if st, err := os.Stat(key); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("key file: %v, %v; want mode 0600", st, err)
	}
	if got := must(t, "pubkey", "-key", key); got != pub {
		t.Errorf("pubkey = %q, keygen printed %q", got, pub)
	}
	if _, errOut, code := exec(t, "keygen", "-out", key); code == 0 || !strings.Contains(errOut, "never overwritten") {
		t.Errorf("a second keygen must refuse to overwrite: code %d %q", code, errOut)
	}
}

func TestAddSignVerifyThroughTheCommandLine(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "builds.key")
	pub := must(t, "keygen", "-out", key)
	index := filepath.Join(dir, "builds.json")
	h := func(c string) string { return strings.Repeat(c, 64) }
	must(t, "add", "-index", index, "-meta", describe(t, dir, "v3.10.2-lucx.307", "amd64", h("1"), h("2")))
	must(t, "add", "-index", index, "-meta", describe(t, dir, "v3.10.2-lucx.307", "arm64", h("3"), h("4")))
	must(t, "sign", "-key", key, "-index", index)
	must(t, "verify", "-index", index, "-sig", index+".sig", "-pub", pub)

	raw, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := builds.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if idx.Sequence != 2 || len(idx.Builds) != 2 || idx.Builds[0].Arch != "amd64" {
		t.Errorf("index = %+v", idx)
	}
	if b, kind := idx.Lookup(h("1"), "amd64"); b == nil || kind != builds.Original {
		t.Error("the original hash was not recorded")
	}

	if err := os.WriteFile(index, append(raw, ' '), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, code := exec(t, "verify", "-index", index, "-sig", index+".sig", "-pub", pub); code == 0 {
		t.Error("a modified index verified")
	}
}

func TestSignTakesTheKeyFromAnEnvironmentVariable(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "builds.key")
	pub := must(t, "keygen", "-out", key)
	raw, err := os.ReadFile(key)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("BUILDS_SIGNING_KEY", strings.TrimSpace(string(raw)))
	index := filepath.Join(dir, "builds.json")
	must(t, "add", "-index", index, "-meta", describe(t, dir, "v3.10.2-lucx.307", "amd64", strings.Repeat("1", 64), strings.Repeat("2", 64)))
	must(t, "sign", "-key-env", "BUILDS_SIGNING_KEY", "-index", index)
	must(t, "verify", "-index", index, "-sig", index+".sig", "-pub", pub)
	if _, errOut, code := exec(t, "sign", "-key-env", "NOT_SET", "-index", index); code == 0 || strings.Contains(errOut, strings.TrimSpace(string(raw))) {
		t.Errorf("a missing variable must fail without echoing anything secret: %d %q", code, errOut)
	}
}

func TestSignRefusesAnIndexThatClientsWouldRefuse(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "builds.key")
	must(t, "keygen", "-out", key)
	index := filepath.Join(dir, "builds.json")
	if err := os.WriteFile(index, []byte(`{"schema":1,"sequence":0,"generatedAt":"2026-10-22T00:00:00Z","builds":[]}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := exec(t, "sign", "-key", key, "-index", index); code == 0 || !strings.Contains(errOut, "sequence") {
		t.Errorf("sign must validate first: %d %q", code, errOut)
	}
	if _, err := os.Stat(index + ".sig"); err == nil {
		t.Error("a signature was written for an invalid index")
	}
}

func TestMetaDerivesTheDownloadAddressesItself(t *testing.T) {
	dir := t.TempDir()
	path := describe(t, dir, "v3.10.2-lucx.307", "arm64", strings.Repeat("1", 64), strings.Repeat("2", 64))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"https://github.com/AlexeyLCP/lucx-ui/releases/download/v3.10.2-lucx.307/x-ui-linux-arm64.tar.gz",
		"https://github.com/AllIsA1/lucx-plugins/releases/download/v3.10.2-lucx.307-plugins.1/x-ui-plugins-linux-arm64.tar.gz",
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("meta lacks %s", want)
		}
	}
	if _, errOut, code := exec(t, "meta", "-upstream", "latest", "-arch", "amd64", "-release", "x", "-out", filepath.Join(dir, "x.json")); code == 0 {
		t.Errorf("meta accepted an invalid build: %q", errOut)
	}
}
