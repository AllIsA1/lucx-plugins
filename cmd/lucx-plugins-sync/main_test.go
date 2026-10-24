// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/AllIsA1/lucx-plugins/internal/agent"
	"github.com/AllIsA1/lucx-plugins/internal/builds"
)

func TestParseConfig(t *testing.T) {
	cfg, err := parseConfig(strings.NewReader(`
# comment
PANEL_BINARY=/opt/x-ui/x-ui
SERVICE="my-panel"
STATE_DIR = /var/lib/x
RESTART_AT=05:30
INDEX_URL=https://mirror.example/builds.json
BIN_DIR=/opt/x-ui/bin
SIDECARS_URL=https://mirror.example/sidecars.json
SIDECARS=off
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PanelBinary != "/opt/x-ui/x-ui" || cfg.Service != "my-panel" || cfg.StateDir != "/var/lib/x" || cfg.RestartAt != "05:30" || cfg.IndexURL != "https://mirror.example/builds.json" ||
		cfg.BinDir != "/opt/x-ui/bin" || cfg.SidecarsURL != "https://mirror.example/sidecars.json" || !cfg.NoSidecars {
		t.Errorf("cfg = %+v", cfg)
	}
	for name, doc := range map[string]string{"unknown key": "BOGUS=1\n", "no equals": "PANEL_BINARY\n", "empty value": "SERVICE=\n"} {
		if _, err := parseConfig(strings.NewReader(doc)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func setup(t *testing.T) (args []string, panel string) {
	t.Helper()
	dir := t.TempDir()
	panel = filepath.Join(dir, "x-ui")
	if err := os.WriteFile(panel, []byte("#!/bin/sh\necho mine\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(dir, "config")
	if err := os.WriteFile(conf, []byte("PANEL_BINARY="+panel+"\nSTATE_DIR="+filepath.Join(dir, "state")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return []string{"-config", conf, "-index-url", "http://127.0.0.1:1/builds.json"}, panel
}

func invoke(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(context.Background(), args, &out, &errOut)
	return out.String(), errOut.String(), code
}

func TestAtStartNeverFailsThePanelStart(t *testing.T) {
	base, panel := setup(t)
	_, errOut, code := invoke(t, append(base, "at-start")...)
	if code != 0 {
		t.Errorf("at-start exited %d: it runs before the panel and must not stop it (%s)", code, errOut)
	}
	if raw, _ := os.ReadFile(panel); string(raw) != "#!/bin/sh\necho mine\n" {
		t.Error("the panel binary was touched without a verified list")
	}
}

func TestStatusAndRestoreOnABinaryThatIsNotOurs(t *testing.T) {
	base, _ := setup(t)
	if out, _, code := invoke(t, append(base, "status")...); code != 0 || !strings.Contains(out, "not a known build") {
		t.Errorf("status = %d %q", code, out)
	}
	if out, _, code := invoke(t, append(base, "restore")...); code != 0 || !strings.Contains(out, "nothing to restore") {
		t.Errorf("restore = %d %q", code, out)
	}
}

func TestUnknownCommandAndMissingConfigAreUsageErrors(t *testing.T) {
	if _, _, code := invoke(t, "nonsense"); code != 2 {
		t.Errorf("unknown command: code %d", code)
	}
	if _, errOut, code := invoke(t, "-config", "/no/such/file", "status"); code == 0 || !strings.Contains(errOut, "/no/such/file") {
		t.Errorf("a missing config file must be named: %d %q", code, errOut)
	}
}

func TestIndexURLPrecedenceIsFlagThenEnvironmentThenConfig(t *testing.T) {
	conf := "INDEX_URL=https://config.example/builds.json\n"
	cases := []struct {
		flag, env, want string
	}{
		{"", "", "https://config.example/builds.json"},
		{"", "https://env.example/builds.json", "https://env.example/builds.json"},
		{"https://flag.example/builds.json", "https://env.example/builds.json", "https://flag.example/builds.json"},
	}
	for _, c := range cases {
		cfg, err := parseConfig(strings.NewReader(conf))
		if err != nil {
			t.Fatal(err)
		}
		if got := indexURL(cfg, c.flag, c.env); got != c.want {
			t.Errorf("indexURL(flag %q, env %q) = %q, want %q", c.flag, c.env, got, c.want)
		}
	}
	if got := indexURL(config{}, "", ""); got != "" {
		t.Errorf("with nothing set the agent default must apply, got %q", got)
	}
}

func TestHostGuardRollsBackAHostThatKeepsFailingAndNeverBlocksTheStart(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "lucx-plugin-host")
	state := filepath.Join(dir, "state")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	broken := "#!/bin/sh\nif [ \"$1\" = \"-v\" ]; then echo host-2; exit 0; fi\nexit 1\n"
	previous := "#!/bin/sh\necho host-1\n"
	for path, body := range map[string]string{exe: broken, exe + ".prev": previous} {
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(state, "update.json"), []byte(`{"pending":{"version":"host-2","starts":3}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := invoke(t, "-host-exe", exe, "-host-state", state, "host-guard")
	if code != 0 {
		t.Fatalf("host-guard exited %d (%s): it must never keep the host from starting", code, stderr)
	}
	if got, _ := os.ReadFile(exe); string(got) != previous {
		t.Errorf("binary = %q, want the previous release restored", got)
	}
	if !strings.Contains(stderr, "rolled back") {
		t.Errorf("stderr = %q, want the rollback said", stderr)
	}

	// Nothing pending, or nothing there at all: quiet success.
	for _, args := range [][]string{
		{"-host-exe", exe, "-host-state", state, "host-guard"},
		{"-host-exe", filepath.Join(dir, "missing"), "-host-state", filepath.Join(dir, "none"), "host-guard"},
	} {
		if _, stderr, code := invoke(t, args...); code != 0 {
			t.Errorf("host-guard %v = %d %q", args, code, stderr)
		}
	}
}

// signedHostList serves a signed host.json and one binary, the way the published list does.
func signedHostList(t *testing.T, version string, binary []byte) (base, download, pub string) {
	t.Helper()
	pubKey, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sum := func(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
	arch := runtime.GOARCH
	hashes := map[string]string{"amd64": sum([]byte("other amd64")), "arm64": sum([]byte("other arm64"))}
	hashes[arch] = sum(binary)
	doc := &builds.HostIndex{Schema: 1, Sequence: 1}
	if err := doc.Set(version, hashes); err != nil {
		t.Fatal(err)
	}
	doc.GeneratedAt = time.Now().UTC()
	raw, _ := builds.MarshalHost(doc)
	sig := builds.SignHost(priv, raw)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/list/host.json":
			_, _ = w.Write(raw)
		case r.URL.Path == "/list/host.json.sig":
			_, _ = w.Write([]byte(sig))
		case r.URL.Path == "/dl/"+version+"/lucx-plugin-host-linux-"+arch:
			_, _ = w.Write(binary)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/list", srv.URL + "/dl", base64.StdEncoding.EncodeToString(pubKey)
}

func TestHostInstallPutsTheSignedReleaseInPlaceOnceAndLeavesAnExistingOneAlone(t *testing.T) {
	binary := []byte("#!/bin/sh\necho host-3\n")
	base, download, pub := signedHostList(t, "host-3", binary)
	t.Setenv("LUCX_HOST_UPDATE_BASE", base)
	t.Setenv("LUCX_HOST_UPDATE_DOWNLOAD_BASE", download)
	t.Setenv("LUCX_HOST_UPDATE_PUBKEYS", pub)
	dir := t.TempDir()
	exe := filepath.Join(dir, "bin", "lucx-plugin-host")
	state := filepath.Join(dir, "state")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := invoke(t, "-host-exe", exe, "-host-state", state, "host-install")
	if code != 0 {
		t.Fatalf("host-install = %d: %s", code, stderr)
	}
	if got, _ := os.ReadFile(exe); string(got) != string(binary) {
		t.Errorf("binary = %q", got)
	}
	if !strings.Contains(stdout, "host-3") {
		t.Errorf("stdout = %q, want the installed release named", stdout)
	}

	// A second run (the installer run again) must not touch what is there.
	if err := os.WriteFile(exe, []byte("#!/bin/sh\necho host-9\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	stdout, _, code = invoke(t, "-host-exe", exe, "-host-state", state, "host-install")
	if code != 0 || !strings.Contains(stdout, "already") {
		t.Errorf("second host-install = %d %q, want a quiet 'already installed'", code, stdout)
	}
	if got, _ := os.ReadFile(exe); !strings.Contains(string(got), "host-9") {
		t.Errorf("an installed host was replaced: %q", got)
	}
}

func TestHostInstallFailsLoudlyAndLeavesNothingWhenTheListCannotBeTrusted(t *testing.T) {
	base, download, _ := signedHostList(t, "host-3", []byte("#!/bin/sh\necho host-3\n"))
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	t.Setenv("LUCX_HOST_UPDATE_BASE", base)
	t.Setenv("LUCX_HOST_UPDATE_DOWNLOAD_BASE", download)
	t.Setenv("LUCX_HOST_UPDATE_PUBKEYS", base64.StdEncoding.EncodeToString(otherPub))
	dir := t.TempDir()
	exe := filepath.Join(dir, "lucx-plugin-host")
	_, stderr, code := invoke(t, "-host-exe", exe, "-host-state", filepath.Join(dir, "state"), "host-install")
	if code == 0 || !strings.Contains(stderr, "refused") {
		t.Errorf("host-install with a foreign key = %d %q, want a failure that says why", code, stderr)
	}
	if _, err := os.Stat(exe); err == nil {
		t.Error("a binary was installed from a list nobody trusted")
	}
}

type fakeUpdater struct {
	res     agent.UpdateResult
	err     error
	restart bool
}

func (f *fakeUpdater) Update(_ context.Context, restart bool) (agent.UpdateResult, error) {
	f.restart = restart
	return f.res, f.err
}

func TestUpdateSaysWhatItFoundAndDid(t *testing.T) {
	cfg := config{RestartAt: "04:00"}
	cases := map[string]struct {
		res  agent.UpdateResult
		want []string
	}{
		"nothing newer":    {agent.UpdateResult{Before: "v1-plugins.4", After: "v1-plugins.4"}, []string{"up to date", "v1-plugins.4"}},
		"updated":          {agent.UpdateResult{Before: "v1-plugins.4", After: "v1-plugins.5", Changed: true}, []string{"updated the panel", "v1-plugins.4 -> v1-plugins.5"}},
		"from an upstream": {agent.UpdateResult{After: "v1-plugins.5", Changed: true}, []string{"upstream panel", "-> v1-plugins.5"}},
		"waiting":          {agent.UpdateResult{Before: "v1-plugins.4", After: "v1-plugins.5", Waiting: true}, []string{"v1-plugins.5 is ready", "next panel start", "04:00"}},
	}
	for name, c := range cases {
		var out, errOut bytes.Buffer
		if code := update(context.Background(), &fakeUpdater{res: c.res}, false, cfg, &out, &errOut); code != 0 {
			t.Errorf("%s: exit %d (%s)", name, code, errOut.String())
		}
		for _, w := range c.want {
			if !strings.Contains(out.String(), w) {
				t.Errorf("%s: output %q lacks %q", name, out.String(), w)
			}
		}
	}
}

func TestUpdateFailsWithTheReasonAndPassesTheRestartChoice(t *testing.T) {
	var out, errOut bytes.Buffer
	f := &fakeUpdater{err: errors.New("the published builds list is refused: bad signature")}
	if code := update(context.Background(), f, true, config{}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "refused: bad signature") || out.Len() != 0 {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out.String(), errOut.String())
	}
	f = &fakeUpdater{}
	update(context.Background(), f, true, config{}, &out, &errOut)
	if f.restart {
		t.Error("--no-restart was ignored")
	}
	f = &fakeUpdater{}
	update(context.Background(), f, false, config{}, &out, &errOut)
	if !f.restart {
		t.Error("the panel is restarted by default")
	}
}

func TestUpdateCommandLine(t *testing.T) {
	base, panel := setup(t)
	saved := restartService
	t.Cleanup(func() { restartService = saved })
	if _, _, code := invoke(t, append(base, "update", "--bogus")...); code != 2 {
		t.Errorf("an unknown option: code %d", code)
	}
	if _, _, code := invoke(t, append(base, "status", "--no-restart")...); code != 2 {
		t.Errorf("--no-restart belongs to update only: code %d", code)
	}
	for _, args := range [][]string{{"update"}, {"update", "--no-restart"}} {
		restarted := false
		restartService = func(context.Context, string) error { restarted = true; return nil }
		_, errOut, code := invoke(t, append(append([]string{}, base...), args...)...)
		if code != 1 || errOut == "" {
			t.Errorf("%v with no list to trust: code %d, stderr %q", args, code, errOut)
		}
		if restarted {
			t.Errorf("%v restarted the panel without a list to trust", args)
		}
	}
	if raw, _ := os.ReadFile(panel); string(raw) != "#!/bin/sh\necho mine\n" {
		t.Error("the panel binary was touched")
	}
}

func TestRestartServiceAsksSystemdAndSaysWhyItFailed(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	script := "#!/bin/sh\necho \"$@\" >> " + log + "\nif [ \"$2\" = broken ]; then echo 'Unit broken.service not found.' >&2; exit 5; fi\n"
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := restartService(context.Background(), "x-ui"); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(log); string(raw) != "restart x-ui\n" {
		t.Errorf("systemctl was called with %q", raw)
	}
	err := restartService(context.Background(), "broken")
	if err == nil || !strings.Contains(err.Error(), "Unit broken.service not found") {
		t.Errorf("err = %v: the reason must reach the operator", err)
	}
}
