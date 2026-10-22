// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseConfig(t *testing.T) {
	cfg, err := parseConfig(strings.NewReader(`
# comment
PANEL_BINARY=/opt/x-ui/x-ui
SERVICE="my-panel"
STATE_DIR = /var/lib/x
RESTART_AT=05:30
INDEX_URL=https://mirror.example/builds.json
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PanelBinary != "/opt/x-ui/x-ui" || cfg.Service != "my-panel" || cfg.StateDir != "/var/lib/x" || cfg.RestartAt != "05:30" || cfg.IndexURL != "https://mirror.example/builds.json" {
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
