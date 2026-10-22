// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"archive/tar"
	"context"
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

type testEnv struct {
	m        *Manager
	srv      *httptest.Server
	codeDir  string
	dataDir  string
	regPath  string
	cfg      Config
	mu       sync.Mutex
	paths    []string
	now      time.Time
	archives map[string][]byte
	redirect string
}

func (e *testEnv) requested() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.paths...)
}

func (e *testEnv) clock() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}

func (e *testEnv) advance(d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.now = e.now.Add(d)
}

func goodArchive(t *testing.T, mutate func(m map[string]any)) []byte {
	t.Helper()
	return buildArchive(t, testCommit,
		dir("alerts-"+testCommit[:7]+"/"),
		file("alerts-"+testCommit[:7]+"/lucx-plugin.json", string(manifestJSON(t, mutate))),
		file("alerts-"+testCommit[:7]+"/bin/amd64/alerts", "ELF"),
	)
}

func newEnv(t *testing.T, archives map[string][]byte) *testEnv {
	t.Helper()
	env := &testEnv{archives: archives, now: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)}
	env.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env.mu.Lock()
		env.paths = append(env.paths, r.URL.Path)
		redirect := env.redirect
		env.mu.Unlock()
		if redirect != "" {
			http.Redirect(w, r, redirect, http.StatusFound)
			return
		}
		body, ok := env.archives[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/x-gzip")
		_, _ = w.Write(body)
	}))
	t.Cleanup(env.srv.Close)

	root := t.TempDir()
	env.codeDir = filepath.Join(root, "plugins")
	env.dataDir = filepath.Join(root, "data")
	env.regPath = filepath.Join(root, "etc", "plugins.json")
	env.cfg = Config{
		CodeDir:      env.codeDir,
		DataDir:      env.dataDir,
		RegistryPath: env.regPath,
		ArchiveBase:  env.srv.URL,
		HTTPClient:   env.srv.Client(),
		Limits:       testLimits(),
		PanelVersion: "3.10.2",
		Now:          env.clock,
		StagingTTL:   10 * time.Minute,
	}
	m, err := NewManager(env.cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	env.m = m
	return env
}

const alertsURL = "https://github.com/acme/alerts"

func defaultPerms() []string {
	return []string{"api:clients:read", "events:client.traffic.exhausted"}
}

func TestPreviewAndInstall(t *testing.T) {
	env := newEnv(t, map[string][]byte{"/acme/alerts/tar.gz/HEAD": goodArchive(t, nil)})
	ctx := context.Background()

	p, err := env.m.Preview(ctx, alertsURL)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if p.Token == "" || p.Commit != testCommit || p.Manifest.ID != "traffic-alerts" {
		t.Fatalf("unexpected preview: %+v", p)
	}
	if p.Installed {
		t.Error("Installed = true before install")
	}
	if got := env.requested(); len(got) != 1 || got[0] != "/acme/alerts/tar.gz/HEAD" {
		t.Errorf("requests = %v", got)
	}
	if _, err := os.Stat(filepath.Join(env.codeDir, "traffic-alerts")); !os.IsNotExist(err) {
		t.Error("Preview must not install anything")
	}

	e, err := env.m.Install(p.Token, defaultPerms())
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if e.Enabled {
		t.Error("a freshly installed plugin must be disabled")
	}
	if e.Commit != testCommit || e.Source.Owner != "acme" || !SamePermissionSet(e.Permissions, defaultPerms()) {
		t.Errorf("entry = %+v", e)
	}
	for _, rel := range []string{"lucx-plugin.json", "bin/amd64/alerts"} {
		if _, err := os.Stat(filepath.Join(env.codeDir, "traffic-alerts", filepath.FromSlash(rel))); err != nil {
			t.Errorf("installed file %s: %v", rel, err)
		}
	}
	if left, _ := os.ReadDir(filepath.Join(env.codeDir, ".staging")); len(left) != 0 {
		t.Errorf("staging not cleaned: %v", left)
	}

	again, err := NewManager(env.cfg)
	if err != nil {
		t.Fatalf("NewManager (reopen): %v", err)
	}
	if list := again.List(); len(list) != 1 || list[0].ID != "traffic-alerts" {
		t.Errorf("List after reopen = %+v", list)
	}
}

func TestPreviewPinnedCommit(t *testing.T) {
	other := strings.Repeat("f", 40)
	archives := map[string][]byte{
		"/acme/alerts/tar.gz/" + testCommit: goodArchive(t, nil),
		"/acme/alerts/tar.gz/" + other:      goodArchive(t, nil),
	}
	env := newEnv(t, archives)

	if _, err := env.m.Preview(context.Background(), alertsURL+"/tree/"+testCommit); err != nil {
		t.Fatalf("matching pinned commit rejected: %v", err)
	}
	_, err := env.m.Preview(context.Background(), alertsURL+"/tree/"+other)
	if err == nil || !strings.Contains(err.Error(), "commit") {
		t.Fatalf("err = %v, want a commit mismatch", err)
	}
}

func TestPreviewCommitResolution(t *testing.T) {
	noHeader := buildArchive(t, "", file("alerts-x/lucx-plugin.json", string(manifestJSON(t, nil))))
	archives := map[string][]byte{
		"/acme/alerts/tar.gz/main":          noHeader,
		"/acme/alerts/tar.gz/" + testCommit: noHeader,
	}
	env := newEnv(t, archives)

	if _, err := env.m.Preview(context.Background(), alertsURL+"/tree/main"); err == nil ||
		!strings.Contains(err.Error(), "commit") {
		t.Errorf("branch ref without a commit id in the archive: err = %v, want a commit error", err)
	}
	p, err := env.m.Preview(context.Background(), alertsURL+"/tree/"+testCommit)
	if err != nil {
		t.Fatalf("full-SHA ref without header: %v", err)
	}
	if p.Commit != testCommit {
		t.Errorf("Commit = %q, want the pinned SHA", p.Commit)
	}
}

func TestPreviewDownloadFailures(t *testing.T) {
	env := newEnv(t, map[string][]byte{})
	_, err := env.m.Preview(context.Background(), alertsURL)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("404: err = %v, want a not-found message", err)
	}
	if _, err := env.m.Preview(context.Background(), "https://evil.example/acme/alerts"); err == nil {
		t.Fatal("non-GitHub source accepted")
	}
	if got := env.requested(); len(got) != 1 {
		t.Errorf("a rejected source must not hit the network; requests = %v", got)
	}
}

func TestPreviewRejectsRedirectToAnotherHost(t *testing.T) {
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("redirect target was contacted: %s", r.URL)
	}))
	t.Cleanup(evil.Close)
	env := newEnv(t, nil)
	env.mu.Lock()
	env.redirect = evil.URL + "/x"
	env.mu.Unlock()
	if _, err := env.m.Preview(context.Background(), alertsURL); err == nil {
		t.Fatal("cross-host redirect followed")
	}
}

func TestPreviewRejectsOversizedArchive(t *testing.T) {
	env := newEnv(t, map[string][]byte{"/acme/alerts/tar.gz/HEAD": goodArchive(t, nil)})
	env.cfg.Limits.MaxArchiveBytes = 64
	m, err := NewManager(env.cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Preview(context.Background(), alertsURL)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("err = %v, want a size error", err)
	}
}

func TestPreviewManifestProblems(t *testing.T) {
	cases := map[string][]byte{
		"missing manifest": buildArchive(t, testCommit, file("r/readme.md", "hi")),
		"invalid manifest": buildArchive(t, testCommit, file("r/lucx-plugin.json", `{"id":"../x"}`)),
		"incompatible": buildArchive(t, testCommit, file("r/lucx-plugin.json",
			string(manifestJSON(t, func(m map[string]any) { m["minPanelVersion"] = "9.0.0" })))),
		"oversize manifest": buildArchive(t, testCommit, file("r/lucx-plugin.json",
			strings.Repeat(" ", MaxManifestBytes+1))),
	}
	for name, archive := range cases {
		t.Run(name, func(t *testing.T) {
			env := newEnv(t, map[string][]byte{"/acme/alerts/tar.gz/HEAD": archive})
			if _, err := env.m.Preview(context.Background(), alertsURL); err == nil {
				t.Fatal("Preview accepted a bad plugin")
			}
			left, _ := os.ReadDir(filepath.Join(env.codeDir, ".staging"))
			if len(left) != 0 {
				t.Errorf("failed Preview left staging data: %v", left)
			}
		})
	}
}

func TestPreviewFlagsDangerousPermissions(t *testing.T) {
	archive := goodArchive(t, func(m map[string]any) {
		m["permissions"] = []string{"network", "api:clients:write", "api:clients:read"}
	})
	env := newEnv(t, map[string][]byte{"/acme/alerts/tar.gz/HEAD": archive})
	p, err := env.m.Preview(context.Background(), alertsURL)
	if err != nil {
		t.Fatal(err)
	}
	if !SamePermissionSet(p.Dangerous, []string{"network", "api:clients:write"}) {
		t.Errorf("Dangerous = %v", p.Dangerous)
	}
}

func TestInstallRequiresExactPermissionConfirmation(t *testing.T) {
	cases := map[string][]string{
		"none":     nil,
		"subset":   {"api:clients:read"},
		"superset": {"api:clients:read", "events:client.traffic.exhausted", "network"},
		"other":    {"network"},
	}
	for name, accepted := range cases {
		t.Run(name, func(t *testing.T) {
			env := newEnv(t, map[string][]byte{"/acme/alerts/tar.gz/HEAD": goodArchive(t, nil)})
			p, err := env.m.Preview(context.Background(), alertsURL)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := env.m.Install(p.Token, accepted); !errors.Is(err, ErrPermissionsMismatch) {
				t.Fatalf("err = %v, want ErrPermissionsMismatch", err)
			}
			if len(env.m.List()) != 0 {
				t.Error("plugin registered despite a permission mismatch")
			}
			if _, err := os.Stat(filepath.Join(env.codeDir, "traffic-alerts")); !os.IsNotExist(err) {
				t.Error("plugin files installed despite a permission mismatch")
			}
			if _, err := env.m.Install(p.Token, defaultPerms()); !errors.Is(err, ErrUnknownToken) {
				t.Errorf("token reuse after a failed confirmation: err = %v, want ErrUnknownToken", err)
			}
		})
	}
}

func TestInstallTokenIsSingleUseAndExpires(t *testing.T) {
	env := newEnv(t, map[string][]byte{"/acme/alerts/tar.gz/HEAD": goodArchive(t, nil)})
	p, _ := env.m.Preview(context.Background(), alertsURL)
	if _, err := env.m.Install(p.Token, defaultPerms()); err != nil {
		t.Fatal(err)
	}
	if _, err := env.m.Install(p.Token, defaultPerms()); !errors.Is(err, ErrUnknownToken) {
		t.Errorf("second use: err = %v, want ErrUnknownToken", err)
	}

	env2 := newEnv(t, map[string][]byte{"/acme/alerts/tar.gz/HEAD": goodArchive(t, nil)})
	p2, _ := env2.m.Preview(context.Background(), alertsURL)
	env2.advance(11 * time.Minute)
	if _, err := env2.m.Install(p2.Token, defaultPerms()); !errors.Is(err, ErrUnknownToken) {
		t.Errorf("expired token: err = %v, want ErrUnknownToken", err)
	}
	if _, err := env2.m.Install("deadbeef", defaultPerms()); !errors.Is(err, ErrUnknownToken) {
		t.Errorf("forged token: err = %v, want ErrUnknownToken", err)
	}
	if left, _ := os.ReadDir(filepath.Join(env2.codeDir, ".staging")); len(left) != 0 {
		t.Errorf("expired staging directory not removed: %v", left)
	}
}

func TestInstallRejectsAlreadyInstalled(t *testing.T) {
	env := newEnv(t, map[string][]byte{"/acme/alerts/tar.gz/HEAD": goodArchive(t, nil)})
	ctx := context.Background()
	p1, _ := env.m.Preview(ctx, alertsURL)
	p2, _ := env.m.Preview(ctx, alertsURL)
	if _, err := env.m.Install(p1.Token, defaultPerms()); err != nil {
		t.Fatal(err)
	}
	if _, err := env.m.Install(p2.Token, defaultPerms()); !errors.Is(err, ErrAlreadyInstalled) {
		t.Fatalf("err = %v, want ErrAlreadyInstalled", err)
	}
	p3, err := env.m.Preview(ctx, alertsURL)
	if err != nil {
		t.Fatal(err)
	}
	if !p3.Installed {
		t.Error("Preview of an installed plugin must report Installed")
	}
	if left, _ := os.ReadDir(filepath.Join(env.codeDir, ".staging")); len(left) != 1 {
		t.Errorf("only the pending preview may remain in staging, got %v", left)
	}
}

func TestSetEnabledPersists(t *testing.T) {
	env := newEnv(t, map[string][]byte{"/acme/alerts/tar.gz/HEAD": goodArchive(t, nil)})
	p, _ := env.m.Preview(context.Background(), alertsURL)
	if _, err := env.m.Install(p.Token, defaultPerms()); err != nil {
		t.Fatal(err)
	}
	if err := env.m.SetEnabled("traffic-alerts", true); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	again, _ := NewManager(env.cfg)
	if list := again.List(); len(list) != 1 || !list[0].Enabled {
		t.Errorf("enabled flag lost after reopen: %+v", list)
	}
	if err := env.m.SetEnabled("missing", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestUninstall(t *testing.T) {
	for _, removeData := range []bool{true, false} {
		env := newEnv(t, map[string][]byte{"/acme/alerts/tar.gz/HEAD": goodArchive(t, nil)})
		p, _ := env.m.Preview(context.Background(), alertsURL)
		if _, err := env.m.Install(p.Token, defaultPerms()); err != nil {
			t.Fatal(err)
		}
		dataDir := filepath.Join(env.dataDir, "traffic-alerts")
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dataDir, "state.db"), []byte("s"), 0o600); err != nil {
			t.Fatal(err)
		}

		if err := env.m.Uninstall("traffic-alerts", removeData); err != nil {
			t.Fatalf("Uninstall(removeData=%v): %v", removeData, err)
		}
		if _, err := os.Stat(filepath.Join(env.codeDir, "traffic-alerts")); !os.IsNotExist(err) {
			t.Errorf("removeData=%v: code directory still exists", removeData)
		}
		if len(env.m.List()) != 0 {
			t.Errorf("removeData=%v: registry entry still present", removeData)
		}
		_, statErr := os.Stat(dataDir)
		if removeData && !os.IsNotExist(statErr) {
			t.Error("data directory must be removed on request")
		}
		if !removeData && statErr != nil {
			t.Errorf("data directory must be kept: %v", statErr)
		}
	}
}

func TestUninstallRejectsUnknownAndHostileIDs(t *testing.T) {
	env := newEnv(t, nil)
	victim := filepath.Join(filepath.Dir(env.codeDir), "victim")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"missing", "../victim", "..", "", "a/b", ".staging"} {
		if err := env.m.Uninstall(id, true); err == nil {
			t.Errorf("Uninstall(%q) succeeded", id)
		}
	}
	if _, err := os.Stat(victim); err != nil {
		t.Errorf("directory outside the plugin dir was touched: %v", err)
	}
}

func TestNewManagerWipesStaleStaging(t *testing.T) {
	env := newEnv(t, map[string][]byte{"/acme/alerts/tar.gz/HEAD": goodArchive(t, nil)})
	if _, err := env.m.Preview(context.Background(), alertsURL); err != nil {
		t.Fatal(err)
	}
	if left, _ := os.ReadDir(filepath.Join(env.codeDir, ".staging")); len(left) != 1 {
		t.Fatalf("expected one pending preview, got %v", left)
	}
	if _, err := NewManager(env.cfg); err != nil {
		t.Fatal(err)
	}
	if left, _ := os.ReadDir(filepath.Join(env.codeDir, ".staging")); len(left) != 0 {
		t.Errorf("a restart must drop pending previews: %v", left)
	}
}

func TestArchiveBaseSettingOverridesConfig(t *testing.T) {
	env := newEnv(t, map[string][]byte{})
	mirror := newEnv(t, map[string][]byte{"/acme/alerts/tar.gz/HEAD": goodArchive(t, nil)})
	if err := env.m.SetArchiveBase(mirror.srv.URL); err != nil {
		t.Fatalf("SetArchiveBase: %v", err)
	}
	if got := env.m.ArchiveBase(); got != mirror.srv.URL {
		t.Errorf("ArchiveBase = %q", got)
	}
	if _, err := env.m.Preview(context.Background(), alertsURL); err != nil {
		t.Fatalf("Preview through the configured mirror: %v", err)
	}
	if got := env.requested(); len(got) != 0 {
		t.Errorf("the default base was contacted: %v", got)
	}
	if err := env.m.SetArchiveBase(""); err != nil {
		t.Fatal(err)
	}
	if got := env.m.ArchiveBase(); got != env.srv.URL {
		t.Errorf("clearing the setting must fall back to the config base, got %q", got)
	}
}

func TestNewManagerFallsBackToDefaultBase(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(Config{
		CodeDir: filepath.Join(root, "p"), DataDir: filepath.Join(root, "d"),
		RegistryPath: filepath.Join(root, "plugins.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := m.ArchiveBase(); got != DefaultArchiveBase {
		t.Errorf("ArchiveBase = %q, want %q", got, DefaultArchiveBase)
	}
}

func TestNewManagerFailsOnCorruptRegistry(t *testing.T) {
	root := t.TempDir()
	reg := filepath.Join(root, "plugins.json")
	_ = os.WriteFile(reg, []byte("{broken"), 0o600)
	_, err := NewManager(Config{CodeDir: filepath.Join(root, "p"), DataDir: filepath.Join(root, "d"), RegistryPath: reg})
	if !errors.Is(err, ErrCorruptRegistry) {
		t.Fatalf("err = %v, want ErrCorruptRegistry", err)
	}
}

func TestPreviewKeepsAtMostMaxPendingPreviews(t *testing.T) {
	env := newEnv(t, map[string][]byte{"/acme/alerts/tar.gz/HEAD": goodArchive(t, nil)})
	var tokens []string
	for i := 0; i < MaxPendingPreviews+2; i++ {
		env.advance(time.Second)
		p, err := env.m.Preview(context.Background(), alertsURL)
		if err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, p.Token)
	}
	left, _ := os.ReadDir(filepath.Join(env.codeDir, ".staging"))
	if len(left) != MaxPendingPreviews {
		t.Fatalf("staging holds %d previews, want %d", len(left), MaxPendingPreviews)
	}
	if _, err := env.m.Install(tokens[0], defaultPerms()); !errors.Is(err, ErrUnknownToken) {
		t.Errorf("oldest preview must be evicted: err = %v", err)
	}
	if _, err := env.m.Install(tokens[len(tokens)-1], defaultPerms()); err != nil {
		t.Errorf("newest preview must survive: %v", err)
	}
}

func installAlerts(t *testing.T, env *testEnv) {
	t.Helper()
	p, err := env.m.Preview(context.Background(), alertsURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.m.Install(p.Token, p.Manifest.Permissions); err != nil {
		t.Fatal(err)
	}
}

func TestMarkDisabledRecordsReasonUntilReEnabled(t *testing.T) {
	env := newEnv(t, map[string][]byte{"/acme/alerts/tar.gz/HEAD": goodArchive(t, nil)})
	installAlerts(t, env)
	if err := env.m.SetEnabled("traffic-alerts", true); err != nil {
		t.Fatal(err)
	}

	if err := env.m.markDisabled("traffic-alerts", "crashed repeatedly"); err != nil {
		t.Fatalf("markDisabled: %v", err)
	}
	again, _ := NewManager(env.cfg)
	e := again.List()[0]
	if e.Enabled || e.DisabledReason != "crashed repeatedly" {
		t.Fatalf("after markDisabled and reopen: %+v", e)
	}

	if err := env.m.SetEnabled("traffic-alerts", true); err != nil {
		t.Fatal(err)
	}
	if e := env.m.List()[0]; !e.Enabled || e.DisabledReason != "" {
		t.Errorf("re-enabling must clear the reason: %+v", e)
	}
	if err := env.m.markDisabled("missing", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("markDisabled of an unknown plugin: err = %v", err)
	}
}

type recordingController struct {
	codeDirExisted bool
	codeDir        string
	stopped        []string
	err            error
}

func (c *recordingController) StopPlugin(_ context.Context, id string) error {
	_, statErr := os.Stat(filepath.Join(c.codeDir, id))
	c.codeDirExisted = statErr == nil
	c.stopped = append(c.stopped, id)
	return c.err
}

func TestUninstallStopsTheProcessBeforeDeletingCode(t *testing.T) {
	env := newEnv(t, map[string][]byte{"/acme/alerts/tar.gz/HEAD": goodArchive(t, nil)})
	installAlerts(t, env)
	ctrl := &recordingController{codeDir: env.codeDir}
	env.m.SetProcessController(ctrl)

	if err := env.m.Uninstall("traffic-alerts", false); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if len(ctrl.stopped) != 1 || ctrl.stopped[0] != "traffic-alerts" {
		t.Fatalf("stopped = %v", ctrl.stopped)
	}
	if !ctrl.codeDirExisted {
		t.Error("the process must be stopped while its code still exists")
	}
	if len(env.m.List()) != 0 {
		t.Error("plugin still registered")
	}
}

func TestUninstallKeepsThePluginWhenItCannotBeStopped(t *testing.T) {
	env := newEnv(t, map[string][]byte{"/acme/alerts/tar.gz/HEAD": goodArchive(t, nil)})
	installAlerts(t, env)
	env.m.SetProcessController(&recordingController{codeDir: env.codeDir, err: errors.New("systemctl failed")})

	if err := env.m.Uninstall("traffic-alerts", true); err == nil {
		t.Fatal("Uninstall succeeded although the process could not be stopped")
	}
	if len(env.m.List()) != 1 {
		t.Error("a plugin whose process may still run must stay registered")
	}
	if _, err := os.Stat(filepath.Join(env.codeDir, "traffic-alerts")); err != nil {
		t.Errorf("code directory was removed under a possibly running process: %v", err)
	}
}

func pinnedEnv(t *testing.T, mutate func(m map[string]any)) (*testEnv, PinnedInstall) {
	t.Helper()
	manifest := manifestJSON(t, func(m map[string]any) {
		m["scope"] = "node"
		if mutate != nil {
			mutate(m)
		}
	})
	root := "alerts-" + testCommit[:7] + "/"
	archive := buildArchive(t, testCommit,
		dir(root),
		file(root+ManifestFile, string(manifest)),
		tarEntry{name: root + "bin/amd64/alerts", typ: tar.TypeReg, body: "ELF", mode: 0o755},
	)
	env := newEnv(t, map[string][]byte{"/acme/alerts/tar.gz/" + testCommit: archive})
	sum := sha256.Sum256(manifest)
	return env, PinnedInstall{
		URL:            "https://github.com/acme/alerts",
		Commit:         testCommit,
		ManifestSHA256: hex.EncodeToString(sum[:]),
		Permissions:    []string{"api:clients:read", "events:client.traffic.exhausted"},
	}
}

func assertNothingStaged(t *testing.T, env *testEnv) {
	t.Helper()
	if left, _ := os.ReadDir(filepath.Join(env.codeDir, ".staging")); len(left) != 0 {
		t.Errorf("a refused install left staging data: %v", left)
	}
	if len(env.m.List()) != 0 {
		t.Errorf("a refused install registered a plugin: %+v", env.m.List())
	}
}

func TestInstallPinnedInstallsExactlyWhatTheMasterApproved(t *testing.T) {
	env, req := pinnedEnv(t, nil)
	e, err := env.m.InstallPinned(context.Background(), req)
	if err != nil {
		t.Fatalf("InstallPinned: %v", err)
	}
	if !e.ManagedByMaster || e.Enabled || e.Commit != testCommit || e.Scope != ScopeNode {
		t.Errorf("entry = %+v: it must be marked as master-managed and installed disabled", e)
	}
	if _, err := os.Stat(filepath.Join(env.codeDir, "traffic-alerts", "bin", "amd64", "alerts")); err != nil {
		t.Errorf("plugin files not in place: %v", err)
	}
	if got := env.requested(); len(got) != 1 || got[0] != "/acme/alerts/tar.gz/"+testCommit {
		t.Errorf("the node must download the pinned commit, requested %v", got)
	}
	if left, _ := os.ReadDir(filepath.Join(env.codeDir, ".staging")); len(left) != 0 {
		t.Errorf("staging not cleaned: %v", left)
	}
	again, _ := NewManager(env.cfg)
	if list := again.List(); len(list) != 1 || !list[0].ManagedByMaster {
		t.Errorf("the managed flag must persist: %+v", list)
	}
}

func TestInstallPinnedRefusesAnythingTheMasterDidNotApprove(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(req *PinnedInstall)
		want   error
	}{
		{"another manifest", func(r *PinnedInstall) { r.ManifestSHA256 = strings.Repeat("0", 64) }, ErrManifestMismatch},
		{"a missing permission", func(r *PinnedInstall) { r.Permissions = []string{"api:clients:read"} }, ErrPermissionsMismatch},
		{"an extra permission", func(r *PinnedInstall) { r.Permissions = append(r.Permissions, "network") }, ErrPermissionsMismatch},
		{"no permissions at all", func(r *PinnedInstall) { r.Permissions = nil }, ErrPermissionsMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, req := pinnedEnv(t, nil)
			tc.mutate(&req)
			if _, err := env.m.InstallPinned(context.Background(), req); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			assertNothingStaged(t, env)
		})
	}
}

func TestInstallPinnedRejectsScopesAndSources(t *testing.T) {
	env, req := pinnedEnv(t, func(m map[string]any) { m["scope"] = "master" })
	if _, err := env.m.InstallPinned(context.Background(), req); !errors.Is(err, ErrScopeNotAllowed) {
		t.Errorf("a master-only plugin must not be installed on a node: %v", err)
	}
	assertNothingStaged(t, env)

	env2, req2 := pinnedEnv(t, func(m map[string]any) { m["scope"] = "both" })
	if _, err := env2.m.InstallPinned(context.Background(), req2); err != nil {
		t.Errorf("scope both must be accepted: %v", err)
	}

	env3, req3 := pinnedEnv(t, nil)
	for _, bad := range []string{"main", "abc123", strings.Repeat("g", 40), strings.ToUpper(testCommit)[:39], ""} {
		r := req3
		r.Commit = bad
		if _, err := env3.m.InstallPinned(context.Background(), r); err == nil {
			t.Errorf("commit %q accepted: a branch name can move, only a full SHA is a pin", bad)
		}
	}
	r := req3
	r.URL = "https://evil.example/acme/alerts"
	if _, err := env3.m.InstallPinned(context.Background(), r); err == nil {
		t.Error("a non-GitHub source was accepted")
	}
	if got := env3.requested(); len(got) != 0 {
		t.Errorf("invalid requests must not reach the network: %v", got)
	}
}

func TestInstallPinnedRefusesAnArchiveThatIsNotThePinnedCommit(t *testing.T) {
	env, req := pinnedEnv(t, nil)
	other := strings.Repeat("f", 40)
	req.Commit = other
	env.archives["/acme/alerts/tar.gz/"+other] = env.archives["/acme/alerts/tar.gz/"+testCommit] // its header still says testCommit
	if _, err := env.m.InstallPinned(context.Background(), req); err == nil || !strings.Contains(err.Error(), "commit") {
		t.Fatalf("err = %v, want a commit mismatch", err)
	}
	assertNothingStaged(t, env)
}

func TestInstallPinnedDoesNotReplaceAnInstalledPlugin(t *testing.T) {
	env, req := pinnedEnv(t, nil)
	if _, err := env.m.InstallPinned(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := env.m.InstallPinned(context.Background(), req); !errors.Is(err, ErrAlreadyInstalled) {
		t.Errorf("second install: %v", err)
	}
	if left, _ := os.ReadDir(filepath.Join(env.codeDir, ".staging")); len(left) != 0 {
		t.Errorf("staging not cleaned: %v", left)
	}
}

// With DynamicUser systemd makes <data>/<id> a link to the real state directory.
func TestUninstallRemovesDataBehindTheStateDirectoryLink(t *testing.T) {
	env := newEnv(t, map[string][]byte{"/acme/alerts/tar.gz/HEAD": goodArchive(t, nil)})
	p, _ := env.m.Preview(context.Background(), alertsURL)
	if _, err := env.m.Install(p.Token, defaultPerms()); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(t.TempDir(), "private", "traffic-alerts")
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "state.db"), []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(env.dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(env.dataDir, "traffic-alerts")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	if err := env.m.Uninstall("traffic-alerts", true); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{link, real} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Errorf("%s survived Uninstall(removeData): a reinstall would inherit the old data (%v)", path, err)
		}
	}
}

func TestUninstallDoesNotFollowALinkToSomewhereElse(t *testing.T) {
	env := newEnv(t, map[string][]byte{"/acme/alerts/tar.gz/HEAD": goodArchive(t, nil)})
	p, _ := env.m.Preview(context.Background(), alertsURL)
	if _, err := env.m.Install(p.Token, defaultPerms()); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(t.TempDir(), "precious")
	if err := os.MkdirAll(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(elsewhere, "keep"), []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(env.dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(env.dataDir, "traffic-alerts")
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Fatal(err)
	}

	if err := env.m.Uninstall("traffic-alerts", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "keep")); err != nil {
		t.Errorf("a link to a directory that is not the plugin's own was followed and emptied: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Errorf("the link itself must go: %v", err)
	}
}
