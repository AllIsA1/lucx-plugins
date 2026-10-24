// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// catalogServer publishes a signed catalog and counts how often it is asked.
type catalogServer struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	index  []byte
	sig    string
	hits   int
	status int
	// sigStatus, when set, is the answer for the signature file only.
	sigStatus int
	extra     string
}

func newCatalogServer(t *testing.T) *catalogServer {
	t.Helper()
	c := &catalogServer{t: t}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		defer c.mu.Unlock()
		if r.URL.Path == "/"+CatalogIndexFile {
			c.hits++
		}
		if c.status != 0 {
			w.WriteHeader(c.status)
			return
		}
		if r.URL.Path == "/"+CatalogSigFile && c.sigStatus != 0 {
			w.WriteHeader(c.sigStatus)
			return
		}
		switch r.URL.Path {
		case "/" + CatalogIndexFile:
			_, _ = w.Write(append(c.index, []byte(c.extra)...))
		case "/" + CatalogSigFile:
			_, _ = w.Write([]byte(c.sig + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func (c *catalogServer) publish(priv ed25519.PrivateKey, index []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.index, c.sig = index, SignCatalog(priv, index)
}

func (c *catalogServer) requests() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits
}

type catalogEnv struct {
	*testEnv
	cat  *catalogServer
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

func newCatalogEnv(t *testing.T, archives map[string][]byte) *catalogEnv {
	t.Helper()
	e := &catalogEnv{testEnv: newEnv(t, archives), cat: newCatalogServer(t)}
	e.pub, e.priv = newCatalogKey(t)
	e.reopen(t)
	return e
}

// reopen builds a new Manager over the same files, as a panel restart does.
func (e *catalogEnv) reopen(t *testing.T) {
	t.Helper()
	cfg := e.cfg
	cfg.CatalogKeys = []ed25519.PublicKey{e.pub}
	cfg.CatalogBase = e.cat.srv.URL
	cfg.CatalogMaxAge = time.Hour
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.m = m
}

func (e *catalogEnv) publish(t *testing.T, mutate func(idx map[string]any)) {
	t.Helper()
	e.cat.publish(e.priv, catalogJSON(t, mutate))
}

func entries(plugins ...map[string]any) func(map[string]any) {
	return func(idx map[string]any) {
		list := make([]any, len(plugins))
		for i, p := range plugins {
			list[i] = p
		}
		idx["plugins"] = list
	}
}

func sequenceOf(n int) func(map[string]any) {
	return func(idx map[string]any) { idx["sequence"] = n }
}

func combine(fs ...func(map[string]any)) func(map[string]any) {
	return func(idx map[string]any) {
		for _, f := range fs {
			f(idx)
		}
	}
}

func TestCatalogWithoutAKeyIsNotConfiguredAndAsksNoOne(t *testing.T) {
	env := newCatalogEnv(t, nil)
	cfg := env.cfg
	cfg.CatalogBase = env.cat.srv.URL
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	view := m.Catalog(context.Background(), true)
	if view.State != CatalogUnconfigured || len(view.Plugins) != 0 || env.cat.requests() != 0 {
		t.Errorf("view = %+v after %d requests", view, env.cat.requests())
	}
}

func TestCatalogListsSignedPluginsWithTheirLocalStatus(t *testing.T) {
	env := newCatalogEnv(t, map[string][]byte{"/acme/alerts/tar.gz/HEAD": goodArchive(t, nil)})
	ctx := context.Background()
	p, err := env.m.Preview(ctx, alertsURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.m.Install(p.Token, defaultPerms()); err != nil {
		t.Fatal(err)
	}
	same := goodCatalogEntry("traffic-alerts")
	same["repo"], same["commit"] = alertsURL, testCommit
	newer := goodCatalogEntry("traffic-alerts")
	newer["repo"], newer["commit"] = alertsURL, commitV2
	other := goodCatalogEntry("traffic-alerts")
	other["repo"] = "https://github.com/evil/alerts"
	fresh := goodCatalogEntry("block-ads")

	statusOf := func(entry map[string]any) string {
		env.publish(t, entries(entry, fresh))
		view := env.m.Catalog(ctx, true)
		if view.State != CatalogOK || len(view.Plugins) != 2 {
			t.Fatalf("view = %+v", view)
		}
		if view.Plugins[1].ID != "block-ads" || view.Plugins[1].Status != CatalogAvailable {
			t.Errorf("a plugin that is not installed = %+v", view.Plugins[1])
		}
		return view.Plugins[0].Status
	}
	if got := statusOf(same); got != CatalogInstalled {
		t.Errorf("the installed commit is %q", got)
	}
	if got := statusOf(newer); got != CatalogUpdateAvailable {
		t.Errorf("a newer commit is %q", got)
	}
	if got := statusOf(other); got != CatalogIDConflict {
		t.Errorf("the same id from another repository is %q", got)
	}
}

func TestCatalogIsCachedAndRefetchedWhenAskedOrOld(t *testing.T) {
	env := newCatalogEnv(t, nil)
	env.publish(t, nil)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if view := env.m.Catalog(ctx, false); view.State != CatalogOK {
			t.Fatalf("view = %+v", view)
		}
	}
	if env.cat.requests() != 1 {
		t.Fatalf("%d fetches for three reads inside the max age, want 1", env.cat.requests())
	}
	env.m.Catalog(ctx, true)
	if env.cat.requests() != 2 {
		t.Errorf("a forced refresh made %d fetches in total, want 2", env.cat.requests())
	}
	env.advance(2 * time.Hour)
	env.m.Catalog(ctx, false)
	if env.cat.requests() != 3 {
		t.Errorf("an old cache made %d fetches in total, want 3", env.cat.requests())
	}
}

func TestCatalogSurvivesARestartAndAnOutage(t *testing.T) {
	env := newCatalogEnv(t, nil)
	env.publish(t, nil)
	ctx := context.Background()
	if view := env.m.Catalog(ctx, true); view.State != CatalogOK {
		t.Fatalf("view = %+v", view)
	}
	env.cat.mu.Lock()
	env.cat.status = http.StatusInternalServerError
	env.cat.mu.Unlock()

	env.reopen(t)
	if view := env.m.Catalog(ctx, false); view.State != CatalogOK || len(view.Plugins) != 2 || env.cat.requests() != 1 {
		t.Errorf("a fresh cache after a restart = %+v (%d fetches)", view, env.cat.requests())
	}
	view := env.m.Catalog(ctx, true)
	if view.State != CatalogStale || len(view.Plugins) != 2 || !strings.Contains(view.Error, "500") {
		t.Errorf("during an outage = %+v, want the cached catalog marked stale with the reason", view)
	}

	empty := newCatalogEnv(t, nil)
	empty.cat.mu.Lock()
	empty.cat.status = http.StatusInternalServerError
	empty.cat.mu.Unlock()
	if view := empty.m.Catalog(ctx, true); view.State != CatalogError || len(view.Plugins) != 0 {
		t.Errorf("an outage with nothing cached = %+v", view)
	}
}

func TestCatalogRefusesWhatIsNotSignedByATrustedKey(t *testing.T) {
	env := newCatalogEnv(t, nil)
	ctx := context.Background()
	env.publish(t, entries(goodCatalogEntry("block-ads")))
	if view := env.m.Catalog(ctx, true); view.State != CatalogOK {
		t.Fatalf("view = %+v", view)
	}

	_, stranger := newCatalogKey(t)
	env.cat.publish(stranger, catalogJSON(t, combine(entries(goodCatalogEntry("evil")), sequenceOf(9))))
	view := env.m.Catalog(ctx, true)
	if view.State != CatalogStale || len(view.Plugins) != 1 || view.Plugins[0].ID != "block-ads" || !strings.Contains(view.Error, "signature") {
		t.Errorf("a catalog signed by a stranger = %+v, want the old catalog kept", view)
	}

	// The same index with a good signature and an edited byte.
	env.publish(t, nil)
	env.cat.mu.Lock()
	env.cat.extra = " "
	env.cat.mu.Unlock()
	if view := env.m.Catalog(ctx, true); view.State != CatalogStale {
		t.Errorf("an edited index = %+v", view)
	}
}

func TestCatalogKeyRotationAcceptsAnyListedKey(t *testing.T) {
	env := newCatalogEnv(t, nil)
	spare, spareKey := newCatalogKey(t)
	cfg := env.cfg
	cfg.CatalogKeys = []ed25519.PublicKey{env.pub, spare}
	cfg.CatalogBase = env.cat.srv.URL
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	env.cat.publish(spareKey, catalogJSON(t, nil))
	if view := m.Catalog(context.Background(), true); view.State != CatalogOK {
		t.Errorf("signed by the spare key = %+v", view)
	}
}

func TestCatalogCannotBeRolledBackToAnOlderIndex(t *testing.T) {
	env := newCatalogEnv(t, nil)
	ctx := context.Background()
	env.publish(t, combine(entries(goodCatalogEntry("block-ads")), sequenceOf(7)))
	if view := env.m.Catalog(ctx, true); view.State != CatalogOK || view.Sequence != 7 {
		t.Fatalf("view = %+v", view)
	}
	env.publish(t, combine(entries(goodCatalogEntry("old-one")), sequenceOf(5)))
	view := env.m.Catalog(ctx, true)
	if view.State != CatalogStale || view.Sequence != 7 || !strings.Contains(view.Error, "older") {
		t.Errorf("an older, validly signed index = %+v", view)
	}
	env.reopen(t)
	if view := env.m.Catalog(ctx, true); view.State != CatalogStale || view.Sequence != 7 {
		t.Errorf("the same attack after a restart = %+v", view)
	}
	env.publish(t, combine(entries(goodCatalogEntry("block-ads")), sequenceOf(7)))
	if view := env.m.Catalog(ctx, true); view.State != CatalogOK {
		t.Errorf("the same sequence again must be fine: %+v", view)
	}
	env.publish(t, combine(entries(goodCatalogEntry("block-ads")), sequenceOf(8)))
	if view := env.m.Catalog(ctx, true); view.State != CatalogOK || view.Sequence != 8 {
		t.Errorf("a newer one = %+v", view)
	}
}

func TestCatalogRefusesAnOversizedIndex(t *testing.T) {
	env := newCatalogEnv(t, nil)
	env.cat.publish(env.priv, []byte(strings.Repeat(" ", MaxCatalogBytes+10)))
	if view := env.m.Catalog(context.Background(), true); view.State != CatalogError {
		t.Errorf("view = %+v", view)
	}
}

func TestCatalogSourceCanBeMovedToAMirror(t *testing.T) {
	env := newCatalogEnv(t, nil)
	mirror := newCatalogServer(t)
	mirror.publish(env.priv, catalogJSON(t, entries(goodCatalogEntry("only-on-mirror"))))

	if err := env.m.SetCatalogBase("http://evil.example"); err == nil {
		t.Error("plain-http non-loopback base accepted")
	}
	if err := env.m.SetCatalogBase(mirror.srv.URL); err != nil {
		t.Fatal(err)
	}
	view := env.m.Catalog(context.Background(), true)
	if view.State != CatalogOK || len(view.Plugins) != 1 || view.Plugins[0].ID != "only-on-mirror" {
		t.Errorf("view from the mirror = %+v", view)
	}
	if got := env.m.CustomCatalogBase(); got != mirror.srv.URL {
		t.Errorf("CustomCatalogBase = %q", got)
	}
	if err := env.m.SetCatalogBase(""); err != nil || env.m.CustomCatalogBase() != "" {
		t.Errorf("clearing the mirror: %v", err)
	}
}

func manifestHash(t *testing.T, mutate func(m map[string]any)) string {
	t.Helper()
	sum := sha256.Sum256(manifestJSON(t, mutate))
	return hex.EncodeToString(sum[:])
}

// catalogEntryFor describes the traffic-alerts archive at a commit as the catalog would.
func catalogEntryFor(t *testing.T, commit string, mutate func(m map[string]any)) map[string]any {
	t.Helper()
	e := goodCatalogEntry("traffic-alerts")
	e["repo"], e["commit"] = alertsURL, commit
	e["manifestSha256"] = manifestHash(t, mutate)
	e["version"], e["type"], e["scope"] = "1.2.0", "process", "master"
	e["permissions"] = defaultPerms()
	return e
}

func TestPreviewCatalogInstallsOnlyWhatTheCatalogSigned(t *testing.T) {
	env := newCatalogEnv(t, map[string][]byte{"/acme/alerts/tar.gz/" + testCommit: goodArchive(t, nil)})
	ctx := context.Background()
	entry := catalogEntryFor(t, testCommit, nil)
	env.publish(t, entries(entry))

	p, err := env.m.PreviewCatalog(ctx, "traffic-alerts")
	if err != nil {
		t.Fatal(err)
	}
	if p.Commit != testCommit || p.Manifest.ID != "traffic-alerts" || p.Token == "" {
		t.Fatalf("preview = %+v", p)
	}
	if _, err := env.m.Install(p.Token, defaultPerms()); err != nil {
		t.Fatal(err)
	}
	if got := env.requested(); len(got) == 0 || got[len(got)-1] != "/acme/alerts/tar.gz/"+testCommit {
		t.Errorf("downloads = %v, want the pinned commit, not a branch", got)
	}

	if _, err := env.m.PreviewCatalog(ctx, "no-such-plugin"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a plugin that is not listed: %v", err)
	}
}

func TestPreviewCatalogRefusesAnArchiveThatDiffersFromTheIndex(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name  string
		entry func(t *testing.T) map[string]any
	}{
		{"another manifest", func(t *testing.T) map[string]any {
			e := catalogEntryFor(t, testCommit, nil)
			e["manifestSha256"] = strings.Repeat("0", 64)
			return e
		}},
		{"other permissions", func(t *testing.T) map[string]any {
			e := catalogEntryFor(t, testCommit, nil)
			e["permissions"] = []string{"api:clients:read"}
			return e
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := newCatalogEnv(t, map[string][]byte{"/acme/alerts/tar.gz/" + testCommit: goodArchive(t, nil)})
			env.publish(t, entries(tc.entry(t)))
			if _, err := env.m.PreviewCatalog(ctx, "traffic-alerts"); !errors.Is(err, ErrCatalogMismatch) {
				t.Fatalf("err = %v, want ErrCatalogMismatch", err)
			}
			if left, _ := os.ReadDir(env.codeDir + "/" + stagingDirName); len(left) != 0 {
				t.Errorf("a refused download stayed staged: %v", left)
			}
		})
	}
}

func TestPreviewCatalogUpdateChecksTheNewCommitAgainstTheIndex(t *testing.T) {
	env := newCatalogEnv(t, map[string][]byte{
		"/acme/alerts/tar.gz/HEAD":        archiveAt(t, testCommit, "1.2.0", nil),
		"/acme/alerts/tar.gz/" + commitV2: archiveAt(t, commitV2, "1.3.0", nil),
	})
	ctx := context.Background()
	p, err := env.m.Preview(ctx, alertsURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.m.Install(p.Token, defaultPerms()); err != nil {
		t.Fatal(err)
	}

	newMeta := func(m map[string]any) { m["version"] = "1.3.0" }
	entry := catalogEntryFor(t, commitV2, newMeta)
	entry["version"] = "1.3.0"
	env.publish(t, entries(entry))
	up, err := env.m.PreviewCatalogUpdate(ctx, "traffic-alerts")
	if err != nil {
		t.Fatal(err)
	}
	if up.UpToDate || up.Commit != commitV2 || up.Manifest.Version != "1.3.0" {
		t.Fatalf("update preview = %+v", up)
	}

	entry["manifestSha256"] = strings.Repeat("0", 64)
	env.publish(t, combine(entries(entry), sequenceOf(8)))
	env.m.Catalog(ctx, true)
	if _, err := env.m.PreviewCatalogUpdate(ctx, "traffic-alerts"); !errors.Is(err, ErrCatalogMismatch) {
		t.Errorf("a changed archive: %v, want ErrCatalogMismatch", err)
	}
}

func TestLinkPreviewSaysWhetherTheCatalogVouchesForTheCommit(t *testing.T) {
	env := newCatalogEnv(t, map[string][]byte{"/acme/alerts/tar.gz/HEAD": goodArchive(t, nil)})
	ctx := context.Background()

	if p, err := env.m.Preview(ctx, alertsURL); err != nil || p.Catalog != ListingUnavailable {
		t.Fatalf("before any catalog was read: %+v, %v", p, err)
	}
	listed := catalogEntryFor(t, testCommit, nil)
	env.publish(t, entries(listed))
	env.m.Catalog(ctx, true)
	if p, err := env.m.Preview(ctx, alertsURL); err != nil || p.Catalog != ListingListed {
		t.Errorf("the catalog's own commit: %+v, %v", p, err)
	}
	before := env.cat.requests()
	other := catalogEntryFor(t, commitV2, nil)
	env.publish(t, combine(entries(other), sequenceOf(9)))
	env.m.Catalog(ctx, true)
	if p, err := env.m.Preview(ctx, alertsURL); err != nil || p.Catalog != ListingUnlisted {
		t.Errorf("another commit: %+v, %v", p, err)
	}
	if env.cat.requests() != before+1 {
		t.Error("a link preview must not fetch the catalog itself")
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(env.regPath), "plugins-catalog.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := env.cfg
	cfg.CatalogKeys = nil
	if m, err := NewManager(cfg); err != nil {
		t.Fatal(err)
	} else if p, err := m.Preview(ctx, alertsURL); err != nil || p.Catalog != ListingUnavailable {
		t.Errorf("a panel without catalog keys: %+v, %v", p, err)
	}
}

func TestNewManagerRefusesAnInsecureCatalogBase(t *testing.T) {
	env := newCatalogEnv(t, nil)
	cfg := env.cfg
	cfg.CatalogBase = "http://catalog.example.com"
	if _, err := NewManager(cfg); err == nil {
		t.Fatal("a plain-http catalog base was accepted")
	}
}

func TestACatalogThatIsNotPublishedYetIsNotAnError(t *testing.T) {
	env := newCatalogEnv(t, nil)
	ctx := context.Background()
	env.cat.mu.Lock()
	env.cat.status = http.StatusNotFound
	env.cat.mu.Unlock()
	if view := env.m.Catalog(ctx, true); view.State != CatalogNotPublished || view.Error != "" || len(view.Plugins) != 0 {
		t.Errorf("a 404 with nothing cached = %+v, want not-published and no error text", view)
	}

	env.cat.mu.Lock()
	env.cat.status = 0
	env.cat.mu.Unlock()
	env.publish(t, nil)
	if view := env.m.Catalog(ctx, true); view.State != CatalogOK {
		t.Fatalf("view = %+v", view)
	}
	env.cat.mu.Lock()
	env.cat.status = http.StatusNotFound
	env.cat.mu.Unlock()
	if view := env.m.Catalog(ctx, true); view.State != CatalogStale || len(view.Plugins) != 2 || view.Error == "" {
		t.Errorf("a catalog that disappeared after being read = %+v, want the cached one marked stale", view)
	}
}

func TestACatalogWithoutASignatureFileIsBroken(t *testing.T) {
	env := newCatalogEnv(t, nil)
	env.cat.publish(env.priv, catalogJSON(t, nil))
	env.cat.mu.Lock()
	env.cat.sigStatus = http.StatusNotFound
	env.cat.mu.Unlock()
	if view := env.m.Catalog(context.Background(), true); view.State != CatalogError || !strings.Contains(view.Error, "signature") {
		t.Errorf("view = %+v, want an error: an index without its signature is a broken publication", view)
	}
}

// trackedEntryFor is a catalog entry that vouches for the repository, not for one commit.
func trackedEntryFor(t *testing.T) map[string]any {
	t.Helper()
	e := catalogEntryFor(t, testCommit, nil)
	delete(e, "commit")
	delete(e, "manifestSha256")
	e["track"] = true
	return e
}

func TestTrackingEntryInstallsAndUpdatesFromTheDefaultBranch(t *testing.T) {
	env := newCatalogEnv(t, map[string][]byte{
		"/acme/alerts/tar.gz/HEAD": archiveAt(t, testCommit, "1.2.0", nil),
	})
	ctx := context.Background()
	env.publish(t, entries(trackedEntryFor(t)))

	p, err := env.m.PreviewCatalog(ctx, "traffic-alerts")
	if err != nil {
		t.Fatalf("a tracking entry must install from HEAD: %v", err)
	}
	if _, err := env.m.Install(p.Token, defaultPerms()); err != nil {
		t.Fatal(err)
	}
	if view := env.m.Catalog(ctx, true); view.Plugins[0].Status != CatalogInstalled {
		t.Errorf("status = %q, want installed: there is no signed commit to differ from", view.Plugins[0].Status)
	}

	// The repository moves on without any new catalog entry.
	env.archives["/acme/alerts/tar.gz/HEAD"] = archiveAt(t, commitV2, "1.3.0", func(m map[string]any) { m["version"] = "1.3.0" })
	up, err := env.m.PreviewCatalogUpdate(ctx, "traffic-alerts")
	if err != nil {
		t.Fatal(err)
	}
	if up.UpToDate || up.Commit != commitV2 || up.Manifest.Version != "1.3.0" {
		t.Fatalf("update preview = %+v, want the new HEAD", up)
	}
}

func TestTrackingEntryStillRefusesAnotherPluginId(t *testing.T) {
	env := newCatalogEnv(t, map[string][]byte{
		"/acme/alerts/tar.gz/HEAD": archiveAt(t, testCommit, "1.2.0", func(m map[string]any) { m["id"] = "something-else" }),
	})
	env.publish(t, entries(trackedEntryFor(t)))
	if _, err := env.m.PreviewCatalog(context.Background(), "traffic-alerts"); !errors.Is(err, ErrCatalogMismatch) {
		t.Errorf("err = %v, want ErrCatalogMismatch", err)
	}
}
