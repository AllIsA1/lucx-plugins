// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package agent

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AllIsA1/lucx-plugins/internal/builds"
)

const (
	upstreamTag = "v3.10.2-lucx.307"
	originalSrc = "#!/bin/sh\necho original panel\n"
	oursSrc     = "#!/bin/sh\necho 3.10.2-lucx.307 with plugins\n"
)

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

type rewrite struct{ target *url.URL }

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme, clone.URL.Host, clone.Host = r.target.Scheme, r.target.Host, r.target.Host
	return http.DefaultTransport.RoundTrip(clone)
}

type env struct {
	t        *testing.T
	panel    string
	stateDir string
	priv     ed25519.PrivateKey
	pub      ed25519.PublicKey
	index    *builds.Index

	mu    sync.Mutex
	files map[string][]byte
	hits  map[string]int
	hook  func(path string)

	agent    *Agent
	restarts int
}

func tarball(t *testing.T, entries ...tar.Header) []byte {
	t.Helper()
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	tw := tar.NewWriter(gz)
	for _, h := range entries {
		body := h.Xattrs["body"]
		h.Xattrs = nil
		h.Size = int64(len(body))
		if h.Typeflag == tar.TypeSymlink || h.Typeflag == tar.TypeDir {
			h.Size = 0
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			if _, err := tw.Write([]byte(body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return raw.Bytes()
}

func file(name, body string) tar.Header {
	return tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o755, Xattrs: map[string]string{"body": body}}
}

func standardTar(t *testing.T, body string) []byte { return tarball(t, file("x-ui/x-ui", body)) }

func newEnv(t *testing.T) *env {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	e := &env{t: t, priv: priv, pub: pub, files: map[string][]byte{}, hits: map[string]int{}}
	e.panel = filepath.Join(dir, "x-ui", "x-ui")
	e.stateDir = filepath.Join(dir, "state")
	if err := os.MkdirAll(filepath.Dir(e.panel), 0o755); err != nil {
		t.Fatal(err)
	}
	e.writePanel(originalSrc)
	e.index = &builds.Index{Schema: builds.SchemaVersion, Sequence: 0}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.mu.Lock()
		e.hits[r.URL.Path]++
		body, ok := e.files[r.URL.Path]
		hook := e.hook
		e.mu.Unlock()
		if hook != nil {
			hook(r.URL.Path)
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	target, _ := url.Parse(srv.URL)
	e.agent = New(Config{
		PanelBinary: e.panel,
		StateDir:    e.stateDir,
		Arch:        "amd64",
		Keys:        []ed25519.PublicKey{pub},
		Client:      &http.Client{Transport: rewrite{target}, Timeout: 5 * time.Second},
		Budget:      10 * time.Second,
		Restart:     func(context.Context) error { e.restarts++; return nil },
		Log:         func(format string, args ...any) { t.Logf(format, args...) },
	})
	return e
}

func (e *env) writePanel(src string) {
	e.t.Helper()
	if err := os.WriteFile(e.panel, []byte(src), 0o755); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) panelSrc() string {
	e.t.Helper()
	raw, err := os.ReadFile(e.panel)
	if err != nil {
		e.t.Fatal(err)
	}
	return string(raw)
}

func (e *env) serve(path string, body []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.files[path] = body
}

func (e *env) hitCount(path string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.hits[path]
}

// publish adds a build to the signed index and serves everything for it.
func (e *env) publish(tag, release, original, ours string, tweak func(*builds.Build, *[]byte)) {
	e.t.Helper()
	ourTar := standardTar(e.t, ours)
	origTar := standardTar(e.t, original)
	b := builds.Build{
		Upstream: tag, Arch: "amd64", Release: release,
		OriginalBinarySha256: sum([]byte(original)),
		OriginalArchive: builds.Archive{
			URL:    "https://github.com/" + builds.UpstreamRepo + "/releases/download/" + tag + "/x-ui-linux-amd64.tar.gz",
			SHA256: sum(origTar),
		},
		Build: builds.Archive{
			URL:    "https://github.com/" + builds.OwnRepo + "/releases/download/" + release + "/x-ui-plugins-linux-amd64.tar.gz",
			SHA256: sum(ourTar),
		},
		BinarySha256: sum([]byte(ours)),
	}
	if tweak != nil {
		tweak(&b, &ourTar)
	}
	if err := e.index.Add(b); err != nil {
		e.t.Fatal(err)
	}
	e.serve("/"+builds.OwnRepo+"/releases/download/"+release+"/x-ui-plugins-linux-amd64.tar.gz", ourTar)
	e.serve("/"+builds.UpstreamRepo+"/releases/download/"+tag+"/x-ui-linux-amd64.tar.gz", origTar)
	e.sign()
}

func (e *env) sign() {
	e.t.Helper()
	e.index.GeneratedAt = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	raw, err := builds.Marshal(e.index)
	if err != nil {
		e.t.Fatal(err)
	}
	e.serve("/AllIsA1/lucx-plugins/builds/builds.json", raw)
	e.serve("/AllIsA1/lucx-plugins/builds/builds.json.sig", []byte(builds.Sign(e.priv, raw)+"\n"))
}

func (e *env) archivePath(release string) string {
	return "/" + builds.OwnRepo + "/releases/download/" + release + "/x-ui-plugins-linux-amd64.tar.gz"
}

func (e *env) swapped() bool { return e.panelSrc() == oursSrc }

func TestAtStartSwapsAKnownOriginalForOurBuild(t *testing.T) {
	e := newEnv(t)
	e.publish(upstreamTag, upstreamTag+"-plugins.1", originalSrc, oursSrc, nil)
	if err := e.agent.AtStart(context.Background()); err != nil {
		t.Fatalf("AtStart: %v", err)
	}
	if !e.swapped() {
		t.Fatalf("the panel binary was not replaced: %q", e.panelSrc())
	}
	if st, err := os.Stat(e.panel); err != nil || st.Mode().Perm() != 0o755 {
		t.Errorf("binary mode: %v %v", st, err)
	}
	saved, err := os.ReadFile(filepath.Join(e.stateDir, "originals", sum([]byte(originalSrc)), "x-ui"))
	if err != nil || string(saved) != originalSrc {
		t.Errorf("the original was not kept for uninstall: %q %v", saved, err)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(e.panel), "*.lucx-new")); len(left) != 0 {
		t.Errorf("temporary file left behind: %v", left)
	}
	st := e.agent.Status()
	if st.Ours != sum([]byte(oursSrc)) || st.Original != sum([]byte(originalSrc)) || st.Upstream != upstreamTag {
		t.Errorf("state = %+v", st)
	}
	if err := e.agent.AtStart(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := e.hitCount(e.archivePath(upstreamTag + "-plugins.1")); n != 1 {
		t.Errorf("our archive downloaded %d times, want once", n)
	}
}

func TestAtStartLeavesAnUnrecognisedBinaryAlone(t *testing.T) {
	e := newEnv(t)
	e.publish(upstreamTag, upstreamTag+"-plugins.1", originalSrc, oursSrc, nil)
	e.writePanel("#!/bin/sh\necho my own build\n")
	if err := e.agent.AtStart(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.panelSrc() != "#!/bin/sh\necho my own build\n" {
		t.Error("a binary that is not in the signed list was touched")
	}
	if n := e.hitCount(e.archivePath(upstreamTag + "-plugins.1")); n != 0 {
		t.Errorf("downloaded %d archives for a binary it does not know", n)
	}
}

func TestRefusesAnArchiveThatDoesNotMatchTheSignedHash(t *testing.T) {
	e := newEnv(t)
	e.publish(upstreamTag, upstreamTag+"-plugins.1", originalSrc, oursSrc, nil)
	e.serve(e.archivePath(upstreamTag+"-plugins.1"), standardTar(t, "#!/bin/sh\necho evil\n"))
	_ = e.agent.AtStart(context.Background())
	if e.panelSrc() != originalSrc {
		t.Fatal("an archive with the wrong hash replaced the panel")
	}
	if e.agent.Status().LastError == "" {
		t.Error("the refusal must be recorded")
	}
}

func TestRefusesArchivesThatAreNotExactlyOneBinary(t *testing.T) {
	cases := map[string][]tar.Header{
		"extra file":   {file("x-ui/x-ui", oursSrc), file("x-ui/other", "x")},
		"traversal":    {file("../x-ui", oursSrc)},
		"absolute":     {file("/usr/bin/x-ui", oursSrc)},
		"symlink":      {{Name: "x-ui/x-ui", Typeflag: tar.TypeSymlink, Linkname: "/bin/sh", Mode: 0o755}},
		"wrong name":   {file("x-ui/panel", oursSrc)},
		"no binary":    {{Name: "x-ui/", Typeflag: tar.TypeDir, Mode: 0o755}},
		"two binaries": {file("x-ui/x-ui", oursSrc), file("x-ui/x-ui", oursSrc)},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.publish(upstreamTag, upstreamTag+"-plugins.1", originalSrc, oursSrc, func(b *builds.Build, raw *[]byte) {
				*raw = tarball(t, entries...)
				b.Build.SHA256 = sum(*raw) // the signed hash matches: only the layout is wrong
			})
			_ = e.agent.AtStart(context.Background())
			if e.panelSrc() != originalSrc {
				t.Errorf("%s: the panel was replaced", name)
			}
		})
	}
}

func TestRefusesAnIndexSignedByAnotherKey(t *testing.T) {
	e := newEnv(t)
	e.publish(upstreamTag, upstreamTag+"-plugins.1", originalSrc, oursSrc, nil)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	e.priv = other
	e.sign()
	_ = e.agent.AtStart(context.Background())
	if e.panelSrc() != originalSrc {
		t.Fatal("a list signed by an unknown key was used")
	}
	if _, err := os.Stat(filepath.Join(e.stateDir, "index", "builds.json")); err == nil {
		t.Error("an unverified list was cached")
	}
}

func TestIgnoresAnOlderListAndKeepsTheCachedOne(t *testing.T) {
	e := newEnv(t)
	e.publish(upstreamTag, upstreamTag+"-plugins.1", originalSrc, oursSrc, nil)
	e.publish("v3.10.2-lucx.308", "v3.10.2-lucx.308-plugins.1", "#!/bin/sh\necho o308\n", "#!/bin/sh\necho 3.10.2-lucx.308\n", nil)
	if err := e.agent.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	newer, _ := builds.Marshal(e.index)
	// Roll the published list back to a validly signed but older one.
	old := &builds.Index{Schema: builds.SchemaVersion, Sequence: 1, GeneratedAt: e.index.GeneratedAt, Builds: e.index.Builds[:1]}
	raw, _ := builds.Marshal(old)
	e.serve("/AllIsA1/lucx-plugins/builds/builds.json", raw)
	e.serve("/AllIsA1/lucx-plugins/builds/builds.json.sig", []byte(builds.Sign(e.priv, raw)+"\n"))
	if err := e.agent.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	cached, err := os.ReadFile(filepath.Join(e.stateDir, "index", "builds.json"))
	if err != nil || !bytes.Equal(cached, newer) {
		t.Errorf("a rollback replaced the cached list: %v", err)
	}
}

func TestWorksOfflineFromTheCacheButNeverWithoutOne(t *testing.T) {
	e := newEnv(t)
	e.publish(upstreamTag, upstreamTag+"-plugins.1", originalSrc, oursSrc, nil)
	if err := e.agent.Check(context.Background()); err != nil { // fills the cache and prefetches the build
		t.Fatal(err)
	}
	e.files = map[string][]byte{} // the network is gone
	if err := e.agent.AtStart(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !e.swapped() {
		t.Error("with a verified cache and a prefetched build the swap must work offline")
	}

	cold := newEnv(t)
	cold.publish(upstreamTag, upstreamTag+"-plugins.1", originalSrc, oursSrc, nil)
	cold.files = map[string][]byte{}
	if err := cold.agent.AtStart(context.Background()); err != nil {
		t.Fatal(err)
	}
	if cold.panelSrc() != originalSrc {
		t.Error("without any verified list nothing may change")
	}
}

func TestAbortsWhenThePanelChangesDuringTheDownload(t *testing.T) {
	e := newEnv(t)
	e.publish(upstreamTag, upstreamTag+"-plugins.1", originalSrc, oursSrc, nil)
	e.hook = func(path string) {
		if path == e.archivePath(upstreamTag+"-plugins.1") {
			e.writePanel("#!/bin/sh\necho updated by x-ui update\n")
		}
	}
	_ = e.agent.AtStart(context.Background())
	if e.panelSrc() != "#!/bin/sh\necho updated by x-ui update\n" {
		t.Error("a binary that changed after it was recognised was overwritten")
	}
}

func TestAbortsWhenTheNewBinaryDoesNotRun(t *testing.T) {
	e := newEnv(t)
	broken := "#!/bin/sh\nexit 3\n"
	e.publish(upstreamTag, upstreamTag+"-plugins.1", originalSrc, broken, nil)
	_ = e.agent.AtStart(context.Background())
	if e.panelSrc() != originalSrc {
		t.Fatal("a build that fails to start was installed")
	}
	if e.agent.Status().LastError == "" {
		t.Error("the refusal must be recorded")
	}
}

func TestUpgradesAnOlderBuildOfTheSameRelease(t *testing.T) {
	e := newEnv(t)
	e.publish(upstreamTag, upstreamTag+"-plugins.1", originalSrc, oursSrc, nil)
	if err := e.agent.AtStart(context.Background()); err != nil || !e.swapped() {
		t.Fatalf("first install: %v", err)
	}
	newer := "#!/bin/sh\necho 3.10.2-lucx.307 with plugins, fixed\n"
	e.publish(upstreamTag, upstreamTag+"-plugins.2", originalSrc, newer, nil)
	if err := e.agent.AtStart(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.panelSrc() != newer {
		t.Fatalf("the rebuilt release was not picked up: %q", e.panelSrc())
	}
	if saved, err := os.ReadFile(filepath.Join(e.stateDir, "originals", sum([]byte(originalSrc)), "x-ui")); err != nil || string(saved) != originalSrc {
		t.Errorf("the original must stay available: %v", err)
	}
}

func TestCheckPrefetchesAndApplyRestartsOnlyWhenASwapIsWaiting(t *testing.T) {
	e := newEnv(t)
	e.publish(upstreamTag, upstreamTag+"-plugins.1", originalSrc, oursSrc, nil)
	if err := e.agent.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.panelSrc() != originalSrc {
		t.Fatal("the timer must not touch the running panel's binary")
	}
	if !e.agent.Status().Pending {
		t.Fatal("a recognised original with a build waiting must be reported as pending")
	}
	if err := e.agent.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.restarts != 1 {
		t.Errorf("restarts = %d, want 1", e.restarts)
	}
	if err := e.agent.AtStart(context.Background()); err != nil { // what the restart's ExecStartPre does
		t.Fatal(err)
	}
	if err := e.agent.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.restarts != 1 || e.agent.Status().Pending {
		t.Errorf("nothing waits any more, yet restarts = %d, pending = %v", e.restarts, e.agent.Status().Pending)
	}
}

func TestRestorePutsTheOriginalBack(t *testing.T) {
	e := newEnv(t)
	e.publish(upstreamTag, upstreamTag+"-plugins.1", originalSrc, oursSrc, nil)
	if err := e.agent.AtStart(context.Background()); err != nil || !e.swapped() {
		t.Fatalf("install: %v", err)
	}
	changed, err := e.agent.Restore(context.Background())
	if err != nil || !changed {
		t.Fatalf("Restore = %v, %v", changed, err)
	}
	if e.panelSrc() != originalSrc {
		t.Errorf("the original was not restored: %q", e.panelSrc())
	}
	if changed, err := e.agent.Restore(context.Background()); err != nil || changed {
		t.Errorf("a second Restore must be a no-op: %v %v", changed, err)
	}
}

func TestRestoreDownloadsTheOriginalWhenTheSavedCopyIsGone(t *testing.T) {
	e := newEnv(t)
	e.publish(upstreamTag, upstreamTag+"-plugins.1", originalSrc, oursSrc, nil)
	if err := e.agent.AtStart(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(e.stateDir, "originals")); err != nil {
		t.Fatal(err)
	}
	if changed, err := e.agent.Restore(context.Background()); err != nil || !changed || e.panelSrc() != originalSrc {
		t.Fatalf("Restore from the upstream release: %v %v %q", changed, err, e.panelSrc())
	}
}

func TestRestoreRefusesADownloadWithTheWrongHash(t *testing.T) {
	e := newEnv(t)
	e.publish(upstreamTag, upstreamTag+"-plugins.1", originalSrc, oursSrc, nil)
	if err := e.agent.AtStart(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(e.stateDir, "originals")); err != nil {
		t.Fatal(err)
	}
	e.serve("/"+builds.UpstreamRepo+"/releases/download/"+upstreamTag+"/x-ui-linux-amd64.tar.gz", standardTar(t, "#!/bin/sh\necho evil original\n"))
	if _, err := e.agent.Restore(context.Background()); err == nil {
		t.Fatal("a restore from a mismatching archive succeeded")
	}
	if !e.swapped() {
		t.Error("the panel binary changed although the restore failed")
	}
}

func TestRestoreLeavesAnUnknownBinaryAlone(t *testing.T) {
	e := newEnv(t)
	e.publish(upstreamTag, upstreamTag+"-plugins.1", originalSrc, oursSrc, nil)
	e.writePanel("#!/bin/sh\necho mine\n")
	if changed, err := e.agent.Restore(context.Background()); err != nil || changed {
		t.Errorf("Restore of a binary that is not ours = %v, %v", changed, err)
	}
	if e.panelSrc() != "#!/bin/sh\necho mine\n" {
		t.Error("an unknown binary was replaced")
	}
}

func TestConcurrentRunsSwapOnce(t *testing.T) {
	e := newEnv(t)
	e.publish(upstreamTag, upstreamTag+"-plugins.1", originalSrc, oursSrc, nil)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = e.agent.AtStart(context.Background())
		}()
	}
	wg.Wait()
	if !e.swapped() {
		t.Error("nobody swapped")
	}
	if n := e.hitCount(e.archivePath(upstreamTag + "-plugins.1")); n != 1 {
		t.Errorf("archive downloaded %d times; runs must be serialised and reuse the first download", n)
	}
}

func TestAtStartStaysInsideItsTimeBudget(t *testing.T) {
	e := newEnv(t)
	e.publish(upstreamTag, upstreamTag+"-plugins.1", originalSrc, oursSrc, nil)
	e.hook = func(string) { time.Sleep(3 * time.Second) }
	e.agent.cfg.Budget = 500 * time.Millisecond
	started := time.Now()
	_ = e.agent.AtStart(context.Background())
	if time.Since(started) > 2*time.Second {
		t.Errorf("AtStart took %v with a 500ms budget; the panel must not wait on a slow network", time.Since(started))
	}
	if e.panelSrc() != originalSrc {
		t.Error("nothing verified, nothing replaced")
	}
}

func TestDownloadsAreCapped(t *testing.T) {
	e := newEnv(t)
	e.publish(upstreamTag, upstreamTag+"-plugins.1", originalSrc, oursSrc, nil)
	e.agent.cfg.MaxArchive = 64
	_ = e.agent.AtStart(context.Background())
	if e.panelSrc() != originalSrc || !strings.Contains(e.agent.Status().LastError, "larger") {
		t.Errorf("an oversized archive must be refused: %q", e.agent.Status().LastError)
	}
}

func TestRefusesAGoodBinaryInAnArchiveWhoseHashWasNotSigned(t *testing.T) {
	e := newEnv(t)
	e.publish(upstreamTag, upstreamTag+"-plugins.1", originalSrc, oursSrc, nil)
	// The binary inside is the signed one, but the archive around it is not the one that was signed.
	repacked := tarball(t, tar.Header{Name: "x-ui/x-ui", Typeflag: tar.TypeReg, Mode: 0o755, Uname: "someone", Xattrs: map[string]string{"body": oursSrc}})
	e.serve(e.archivePath(upstreamTag+"-plugins.1"), repacked)
	_ = e.agent.AtStart(context.Background())
	if e.panelSrc() != originalSrc {
		t.Error("an archive that is not the signed file was accepted")
	}
}

func TestRefusesABinaryThatIsNotTheSignedOneEvenInAnArchiveWithTheSignedHash(t *testing.T) {
	e := newEnv(t)
	e.publish(upstreamTag, upstreamTag+"-plugins.1", originalSrc, oursSrc, func(b *builds.Build, raw *[]byte) {
		*raw = standardTar(t, "#!/bin/sh\necho 3.10.2-lucx.307 evil\n")
		b.Build.SHA256 = sum(*raw)
	})
	_ = e.agent.AtStart(context.Background())
	if e.panelSrc() != originalSrc {
		t.Error("a binary other than the signed one was installed")
	}
}

func TestRestoreDoesNotTrustASavedOriginalThatChanged(t *testing.T) {
	e := newEnv(t)
	e.publish(upstreamTag, upstreamTag+"-plugins.1", originalSrc, oursSrc, nil)
	if err := e.agent.AtStart(context.Background()); err != nil {
		t.Fatal(err)
	}
	saved := filepath.Join(e.stateDir, "originals", sum([]byte(originalSrc)), "x-ui")
	if err := os.WriteFile(saved, []byte("#!/bin/sh\necho tampered\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if changed, err := e.agent.Restore(context.Background()); err != nil || !changed {
		t.Fatalf("Restore: %v %v", changed, err)
	}
	if e.panelSrc() != originalSrc {
		t.Errorf("the restored binary is not the original: %q", e.panelSrc())
	}
}
