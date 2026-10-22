// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func installedPlugin(t *testing.T, mode os.FileMode) (Entry, *Manifest, string) {
	t.Helper()
	codeDir := t.TempDir()
	bin := filepath.Join(codeDir, "traffic-alerts", "bin", "amd64")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "alerts"), []byte("ELF"), mode); err != nil {
		t.Fatal(err)
	}
	m, err := ParseManifest(manifestJSON(t, func(m map[string]any) {
		m["permissions"] = []string{"events:client.created", "api:clients:read", "api:clients:read"}
	}))
	if err != nil {
		t.Fatal(err)
	}
	e := Entry{ID: m.ID, Commit: testCommit, ManifestSHA256: "dc319a19308a4ae62e337927cb3c27e93ef65afa9d36ef75f9ba275b9ab0cadd", Permissions: m.Permissions}
	return e, m, codeDir
}

func TestResolveSpec(t *testing.T) {
	e, m, codeDir := installedPlugin(t, 0o755)
	spec, err := ResolveSpec(e, m, codeDir, "/run/x-ui-plugins/traffic-alerts/plugin.sock", "/var/lib/x-ui-plugins/traffic-alerts", "amd64")
	if err != nil {
		t.Fatalf("ResolveSpec: %v", err)
	}
	if want := filepath.Join(codeDir, "traffic-alerts", "bin", "amd64", "alerts"); spec.Command != want {
		t.Errorf("Command = %q, want %q", spec.Command, want)
	}
	if want := []string{"--socket", "/run/x-ui-plugins/traffic-alerts/plugin.sock"}; !slices.Equal(spec.Args, want) {
		t.Errorf("Args = %q, want %q", spec.Args, want)
	}
	if want := filepath.Join(codeDir, "traffic-alerts"); spec.CodeDir != want {
		t.Errorf("CodeDir = %q, want %q", spec.CodeDir, want)
	}
	if want := []string{"api:clients:read", "events:client.created"}; !slices.Equal(spec.Permissions, want) {
		t.Errorf("Permissions = %q, want normalized %q", spec.Permissions, want)
	}
	if spec.Limits != DefaultResourceLimits || spec.DataDir != "/var/lib/x-ui-plugins/traffic-alerts" {
		t.Errorf("spec = %+v", spec)
	}
}

func TestResolveSpecRejectsUnusableBinaries(t *testing.T) {
	e, m, codeDir := installedPlugin(t, 0o755)
	bin := filepath.Join(codeDir, "traffic-alerts", "bin", "amd64", "alerts")

	if _, err := ResolveSpec(e, m, codeDir, "s", "d", "arm64"); err == nil || !strings.Contains(err.Error(), "architecture") {
		t.Errorf("missing arch build: err = %v", err)
	}

	if err := os.Chmod(bin, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveSpec(e, m, codeDir, "s", "d", "amd64"); err == nil || !strings.Contains(err.Error(), "not executable") {
		t.Errorf("non-executable binary: err = %v", err)
	}

	if err := os.Remove(bin); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/bin/sh", bin); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveSpec(e, m, codeDir, "s", "d", "amd64"); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("symlinked binary: err = %v", err)
	}

	if err := os.Remove(bin); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveSpec(e, m, codeDir, "s", "d", "amd64"); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("directory as binary: err = %v", err)
	}
}

func TestResolveSpecRejectsUIPlugin(t *testing.T) {
	e, _, codeDir := installedPlugin(t, 0o755)
	if _, err := ResolveSpec(e, &Manifest{Type: TypeUI}, codeDir, "s", "d", "amd64"); err == nil {
		t.Error("a plugin without run must not resolve to a process")
	}
}

func TestFingerprintChangesWithCommitAndManifest(t *testing.T) {
	base := Entry{Commit: testCommit, ManifestSHA256: "aaaaaaaaaaaaaaaaaaaa"}
	other := base
	other.Commit = strings.Repeat("f", 40)
	changed := base
	changed.ManifestSHA256 = "bbbbbbbbbbbbbbbbbbbb"
	if Fingerprint(base) == Fingerprint(other) || Fingerprint(base) == Fingerprint(changed) {
		t.Error("fingerprint must change when the commit or the manifest changes")
	}
}

func TestRunnerIsolationReports(t *testing.T) {
	cases := []struct {
		name string
		got  Isolation
		want IsolationLevel
	}{
		{"system systemd", NewSystemdRunner().Isolation(), IsolationStrong},
		{"user systemd", newUserSystemdRunner("/r", "/s").Isolation(), IsolationPartial},
		{"unavailable", NewUnavailableRunner("no").Isolation(), IsolationNone},
		{"user only", NewIsolation("x", false, false, true), IsolationPartial},
	}
	for _, c := range cases {
		if c.got.Level != c.want {
			t.Errorf("%s: level = %q, want %q (%+v)", c.name, c.got.Level, c.want, c.got)
		}
	}
	if sys := NewSystemdRunner().Isolation(); !sys.Network || !sys.Filesystem || !sys.User {
		t.Errorf("system systemd must enforce everything: %+v", sys)
	}
	if usr := newUserSystemdRunner("/r", "/s").Isolation(); usr.Network || len(usr.Notes) == 0 {
		t.Errorf("user systemd must admit it cannot filter the network: %+v", usr)
	}
}
