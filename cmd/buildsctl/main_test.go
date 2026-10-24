// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

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

func TestHostSetSignVerifyThroughTheCommandLine(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "builds.key")
	pub := must(t, "keygen", "-out", key)
	index := filepath.Join(dir, "host.json")
	h := func(c string) string { return strings.Repeat(c, 64) }
	must(t, "host-set", "-index", index, "-version", "host-1", "-amd64-sha256", h("1"), "-arm64-sha256", h("2"))
	must(t, "host-set", "-index", index, "-version", "host-2", "-amd64-sha256", h("3"), "-arm64-sha256", h("4"))
	must(t, "host-sign", "-key", key, "-index", index)
	must(t, "host-verify", "-index", index, "-sig", index+".sig", "-pub", pub)

	raw, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := builds.ParseHost(raw)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Version != "host-2" || doc.Sequence != 3 || doc.GeneratedAt.IsZero() {
		t.Errorf("document = %+v, want host-2 at sequence 3 with a time", doc)
	}
	if b, _ := doc.Binary("arm64"); b.SHA256 != h("4") {
		t.Errorf("arm64 = %+v", b)
	}

	if _, _, code := exec(t, "host-set", "-index", index, "-version", "host-1", "-amd64-sha256", h("1"), "-arm64-sha256", h("2")); code == 0 {
		t.Error("going back to an older release was accepted")
	}
	// A builds signature must not pass for the host document and the other way round.
	if _, _, code := exec(t, "verify", "-index", index, "-sig", index+".sig", "-pub", pub); code == 0 {
		t.Error("a host document verified as a builds list")
	}
	if err := os.WriteFile(index, append(raw, ' '), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, code := exec(t, "host-verify", "-index", index, "-sig", index+".sig", "-pub", pub); code == 0 {
		t.Error("a modified host document verified")
	}
}

func TestHostSignRefusesADocumentClientsWouldRefuse(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "builds.key")
	must(t, "keygen", "-out", key)
	index := filepath.Join(dir, "host.json")
	if err := os.WriteFile(index, []byte(`{"schema":1,"sequence":1,"generatedAt":"2026-10-22T00:00:00Z","version":"v1","binaries":[]}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := exec(t, "host-sign", "-key", key, "-index", index); code == 0 || !strings.Contains(errOut, "refusing") {
		t.Errorf("host-sign must validate first: %d %q", code, errOut)
	}
	if _, err := os.Stat(index + ".sig"); err == nil {
		t.Error("a signature was written for an invalid document")
	}
}

func TestHostSetNeedsEveryHash(t *testing.T) {
	index := filepath.Join(t.TempDir(), "host.json")
	if _, _, code := exec(t, "host-set", "-index", index, "-version", "host-1", "-amd64-sha256", strings.Repeat("1", 64)); code == 0 {
		t.Error("a release without the arm64 hash was accepted")
	}
	if _, err := os.Stat(index); err == nil {
		t.Error("a document was written for a refused release")
	}
}

func TestSidecarSetSignVerifyThroughTheCommandLine(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "builds.key")
	pub := must(t, "keygen", "-out", key)
	index := filepath.Join(dir, "sidecars.json")
	h := func(c string) string { return strings.Repeat(c, 64) }
	commit := strings.Repeat("a", 40)
	set := func(version, release string, amd, arm string) (string, string, int) {
		return exec(t, "sidecar-set", "-index", index, "-name", "openflux", "-version", version, "-commit", commit,
			"-release", release, "-amd64-sha256", h(amd), "-arm64-sha256", h(arm))
	}
	if _, errOut, code := set("v0.13.0", "sidecar-openflux-v0.13.0-1", "1", "2"); code != 0 {
		t.Fatal(errOut)
	}
	if _, errOut, code := set("v0.13.1", "sidecar-openflux-v0.13.1-1", "3", "4"); code != 0 {
		t.Fatal(errOut)
	}
	must(t, "sidecar-sign", "-key", key, "-index", index)
	must(t, "sidecar-verify", "-index", index, "-sig", index+".sig", "-pub", pub)

	raw, _ := os.ReadFile(index)
	doc, err := builds.ParseSidecars(raw)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := doc.Find("openflux", "arm64"); s.Version != "v0.13.1" || s.SHA256 != h("4") || doc.Sequence != 2 || doc.GeneratedAt.IsZero() {
		t.Errorf("document = %+v", doc)
	}
	if _, _, code := set("v0.13.0", "sidecar-openflux-v0.13.0-1", "1", "2"); code == 0 {
		t.Error("going back to an older version was accepted")
	}
	// A sidecar signature must not pass for the other documents.
	if _, _, code := exec(t, "host-verify", "-index", index, "-sig", index+".sig", "-pub", pub); code == 0 {
		t.Error("a sidecar document verified as a host document")
	}
	if err := os.WriteFile(index, append(raw, ' '), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, code := exec(t, "sidecar-verify", "-index", index, "-sig", index+".sig", "-pub", pub); code == 0 {
		t.Error("a modified sidecar document verified")
	}
}

func TestSidecarSetNeedsEveryHash(t *testing.T) {
	index := filepath.Join(t.TempDir(), "sidecars.json")
	if _, _, code := exec(t, "sidecar-set", "-index", index, "-name", "openflux", "-version", "v0.13.0", "-commit", strings.Repeat("a", 40),
		"-release", "sidecar-openflux-v0.13.0-1", "-amd64-sha256", strings.Repeat("1", 64)); code == 0 {
		t.Error("a release without the arm64 hash was accepted")
	}
	if _, err := os.Stat(index); err == nil {
		t.Error("a document was written for a refused release")
	}
}
