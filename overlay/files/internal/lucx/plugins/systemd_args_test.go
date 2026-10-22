// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"slices"
	"strings"
	"testing"
)

func testSpec(perms ...string) ProcessSpec {
	return ProcessSpec{
		ID:          "traffic-alerts",
		Fingerprint: testCommit + ".dc319a19308a",
		CodeDir:     "/usr/local/x-ui/plugins/traffic-alerts",
		Command:     "/usr/local/x-ui/plugins/traffic-alerts/bin/amd64/alerts",
		Args:        []string{"--socket", "/run/x-ui-plugins/traffic-alerts/plugin.sock"},
		SocketPath:  "/run/x-ui-plugins/traffic-alerts/plugin.sock",
		DataDir:     "/var/lib/x-ui-plugins/traffic-alerts",
		Permissions: perms,
	}
}

func TestSystemdRunArgsKeepEveryValueAsOneArgument(t *testing.T) {
	spec := testSpec()
	spec.Command = "/usr/local/x-ui/plugins/traffic-alerts/bin/my tool"
	spec.Args = []string{"a b", "$(reboot)", "x;y", "--flag=1 2", ""}
	args := systemdRunArgs(spec, systemdOptions{})

	sep := slices.Index(args, "--")
	if sep < 0 {
		t.Fatal("no -- separator: the command could be parsed as a systemd-run option")
	}
	want := append([]string{spec.Command}, spec.Args...)
	if got := args[sep+1:]; !slices.Equal(got, want) {
		t.Errorf("command line after -- = %q, want %q", got, want)
	}
	for _, a := range args[:sep] {
		if strings.Contains(a, "reboot") || strings.Contains(a, "my tool") {
			t.Errorf("plugin-controlled text leaked into an option: %q", a)
		}
	}
}

func TestSystemdPropertiesNetworkIsolation(t *testing.T) {
	has := func(props []string, want string) bool { return slices.Contains(props, want) }

	closed := systemdProperties(testSpec("api:clients:read"), systemdOptions{})
	if !has(closed, "IPAddressDeny=any") || !has(closed, "IPAddressAllow=localhost") {
		t.Errorf("a plugin without the network permission must be limited to localhost: %v", closed)
	}
	open := systemdProperties(testSpec("api:clients:read", "network"), systemdOptions{})
	for _, p := range open {
		if strings.HasPrefix(p, "IPAddress") {
			t.Errorf("the network permission must lift the IP filter, found %q", p)
		}
	}
}

func TestSystemdPropertiesSandboxInvariants(t *testing.T) {
	props := systemdProperties(testSpec(), systemdOptions{SystemCallFilter: true})
	for _, must := range []string{
		"Type=exec", "DynamicUser=yes", "NoNewPrivileges=yes", "CapabilityBoundingSet=",
		"ProtectSystem=strict", "ProtectHome=yes", "PrivateTmp=yes", "RestrictNamespaces=yes",
		"StateDirectory=x-ui-plugins/traffic-alerts", "RuntimeDirectory=x-ui-plugins/traffic-alerts",
		"SystemCallFilter=@system-service",
	} {
		if !slices.Contains(props, must) {
			t.Errorf("missing sandbox property %q", must)
		}
	}
	for _, p := range props {
		for _, forbidden := range []string{"BindsTo=", "PartOf=", "Requires=", "After=", "User=root"} {
			if strings.HasPrefix(p, forbidden) {
				t.Errorf("%q would tie the plugin to the panel or grant root", p)
			}
		}
	}
}

func TestSystemdPropertiesSystemCallFilterIsOptIn(t *testing.T) {
	for _, p := range systemdProperties(testSpec(), systemdOptions{}) {
		if strings.HasPrefix(p, "SystemCallFilter=") {
			t.Errorf("SystemCallFilter must stay off on systemd older than %d, found %q", systemdFirst, p)
		}
	}
}

func TestSystemdUserModeDropsSystemOnlyProperties(t *testing.T) {
	props := systemdProperties(testSpec(), systemdOptions{User: true, SystemCallFilter: true})
	for _, p := range props {
		for _, systemOnly := range []string{"DynamicUser=", "IPAddress", "SystemCallFilter=", "ProtectHome=yes"} {
			if strings.HasPrefix(p, systemOnly) {
				t.Errorf("user-manager unit must not carry %q", p)
			}
		}
	}
	if !slices.Contains(props, "ProtectHome=read-only") {
		t.Error("user mode keeps /home readable so test code can live there")
	}
	if got := systemdRunArgs(testSpec(), systemdOptions{User: true})[0]; got != "--user" {
		t.Errorf("first argument = %q, want --user", got)
	}
}

func TestSystemdEnvironmentCannotBeSpoofed(t *testing.T) {
	spec := testSpec()
	spec.Env = map[string]string{"LUCX_PLUGIN_ID": "evil", "LUCX_SOCKET": "/tmp/evil.sock", "EXTRA": "1"}
	args := systemdRunArgs(spec, systemdOptions{})
	for _, want := range []string{
		"--setenv=LUCX_PLUGIN_ID=traffic-alerts",
		"--setenv=LUCX_SOCKET=/run/x-ui-plugins/traffic-alerts/plugin.sock",
		"--setenv=LUCX_DATA_DIR=/var/lib/x-ui-plugins/traffic-alerts",
		"--setenv=LUCX_API_VERSION=1",
		"--setenv=EXTRA=1",
	} {
		if !slices.Contains(args, want) {
			t.Errorf("missing %q in %q", want, args)
		}
	}
	for _, a := range args {
		if strings.Contains(a, "evil") {
			t.Errorf("a reserved variable was overridden: %q", a)
		}
	}
}

func TestUnitNameRoundTrip(t *testing.T) {
	if got := unitName("traffic-alerts"); got != "lucx-plugin-traffic-alerts.service" {
		t.Fatalf("unitName = %q", got)
	}
	if id, ok := idFromUnit("lucx-plugin-traffic-alerts.service"); !ok || id != "traffic-alerts" {
		t.Errorf("idFromUnit = %q, %v", id, ok)
	}
	for _, foreign := range []string{"ssh.service", "lucx-plugin-.service", "lucx-plugin-A.service", "lucx-plugin-a.service", "lucx-plugin-../x.service", "lucx-plugin-abc.timer"} {
		if id, ok := idFromUnit(foreign); ok {
			t.Errorf("idFromUnit(%q) accepted a unit that is not ours: %q", foreign, id)
		}
	}
}

func TestParseSystemdVersion(t *testing.T) {
	real := "systemd 259 (259.5-0ubuntu3.4)\n+PAM +AUDIT +SELINUX +APPARMOR\n"
	if v, err := parseSystemdVersion(real); err != nil || v != 259 {
		t.Errorf("parseSystemdVersion = %d, %v", v, err)
	}
	for _, bad := range []string{"", "not systemd 1", "systemd abc"} {
		if _, err := parseSystemdVersion(bad); err == nil {
			t.Errorf("parseSystemdVersion(%q) accepted garbage", bad)
		}
	}
}

// Captured from `systemctl --user show` on a unit started by systemd-run.
const showRunning = `Description=lucx-plugin:probe:abc
ActiveState=active
SubState=running
MainPID=3966348
Result=success
NRestarts=0
ExecMainStatus=0
`

const showMissing = `Description=lucx-nonexistent.service
ActiveState=inactive
SubState=dead
MainPID=0
Result=success
NRestarts=0
ExecMainStatus=0
`

func TestStatusFromShow(t *testing.T) {
	cases := []struct {
		name string
		id   string
		out  string
		want RunStatus
	}{
		{"running", "probe", showRunning, RunStatus{ID: "probe", State: StateRunning, Fingerprint: "abc", PID: 3966348}},
		{"missing unit is stopped without a fingerprint", "nonexistent", showMissing, RunStatus{ID: "nonexistent", State: StateStopped}},
		{
			"failed carries the result", "p", "Description=lucx-plugin:p:f1\nActiveState=failed\nSubState=failed\nMainPID=0\nResult=start-limit-hit\nNRestarts=5\n",
			RunStatus{ID: "p", State: StateFailed, Fingerprint: "f1", Restarts: 5, Detail: "start-limit-hit"},
		},
		{
			"activating is starting", "p", "Description=lucx-plugin:p:f1\nActiveState=activating\nSubState=auto-restart\nMainPID=0\nNRestarts=2\n",
			RunStatus{ID: "p", State: StateStarting, Fingerprint: "f1", Restarts: 2},
		},
		{
			"deactivating is stopping", "p", "Description=lucx-plugin:p:f1\nActiveState=deactivating\nSubState=stop-sigterm\nMainPID=77\n",
			RunStatus{ID: "p", State: StateStopping, Fingerprint: "f1", PID: 77},
		},
		{
			"another unit's description gives no fingerprint", "p", "Description=OpenBSD Secure Shell server\nActiveState=active\nSubState=running\nMainPID=1\n",
			RunStatus{ID: "p", State: StateRunning, PID: 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			blocks := parseShowBlocks(tc.out)
			if len(blocks) != 1 {
				t.Fatalf("blocks = %d, want 1", len(blocks))
			}
			if got := statusFromShow(tc.id, blocks[0]); got != tc.want {
				t.Errorf("status = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseShowBlocksSplitsUnits(t *testing.T) {
	blocks := parseShowBlocks("Id=a.service\nActiveState=active\n\nId=b.service\nActiveState=failed\n\n\n")
	if len(blocks) != 2 || blocks[0]["Id"] != "a.service" || blocks[1]["ActiveState"] != "failed" {
		t.Errorf("blocks = %v", blocks)
	}
}

func TestParseListedIDs(t *testing.T) {
	out := `lucx-plugin-hello.service loaded active running lucx-plugin:hello:abc
● lucx-plugin-broken.service loaded failed failed lucx-plugin:broken:abc
ssh.service loaded active running OpenBSD Secure Shell server
lucx-plugin-Bad_Name.service loaded active running something odd
lucx-plugin-hello.timer loaded active waiting not a service
`
	got := parseListedIDs(out)
	if want := []string{"broken", "hello"}; !slices.Equal(got, want) {
		t.Errorf("ids = %v, want %v", got, want)
	}
}

func TestSystemdRunKeepsFailedUnitsVisible(t *testing.T) {
	args := systemdRunArgs(testSpec(), systemdOptions{})
	if slices.Contains(args, "--collect") {
		t.Error("--collect garbage-collects a unit that hit its start limit; the crash loop would restart forever unseen")
	}
	if !slices.Contains(args, "--property=RestartSec=5") {
		t.Errorf("default restart delay must be 5s: %q", args)
	}
	fast := systemdRunArgs(testSpec(), systemdOptions{RestartSec: "100ms"})
	if !slices.Contains(fast, "--property=RestartSec=100ms") {
		t.Errorf("RestartSec override ignored: %q", fast)
	}
}

func TestSystemdCredentialAndPanelAddress(t *testing.T) {
	spec := testSpec("api:clients:read")
	spec.TokenFile = "/run/x-ui-plugins-secrets/traffic-alerts"
	spec.PanelURL = "https://10.0.0.5:2053/base"
	spec.PanelAddr = "10.0.0.5"
	args := systemdRunArgs(spec, systemdOptions{})

	for _, want := range []string{
		"--property=LoadCredential=panel-token:/run/x-ui-plugins-secrets/traffic-alerts",
		"--property=IPAddressAllow=10.0.0.5",
		"--setenv=LUCX_PANEL_URL=https://10.0.0.5:2053/base",
	} {
		if !slices.Contains(args, want) {
			t.Errorf("missing %q in %q", want, args)
		}
	}

	plain := systemdRunArgs(testSpec(), systemdOptions{})
	for _, a := range plain {
		if strings.Contains(a, "LoadCredential") || strings.Contains(a, "LUCX_PANEL_URL") {
			t.Errorf("no token and no panel URL were given, yet %q is set", a)
		}
	}

	open := testSpec("network")
	open.PanelAddr = "10.0.0.5"
	for _, p := range systemdProperties(open, systemdOptions{}) {
		if strings.HasPrefix(p, "IPAddress") {
			t.Errorf("a plugin with network access needs no IP filter, found %q", p)
		}
	}

	spoof := testSpec()
	spoof.PanelURL = "http://127.0.0.1:2053"
	spoof.Env = map[string]string{"LUCX_PANEL_URL": "http://evil.example"}
	if slices.Contains(systemdRunArgs(spoof, systemdOptions{}), "--setenv=LUCX_PANEL_URL=http://evil.example") {
		t.Error("LUCX_PANEL_URL is reserved and must not be overridable")
	}
}

func TestPanelAddrOf(t *testing.T) {
	cases := map[string]string{
		"http://127.0.0.1:2053":       "",
		"https://localhost:2053/base": "",
		"http://[::1]:2053":           "",
		"https://10.0.0.5:2053/base":  "10.0.0.5",
		"http://[2001:db8::1]:2053":   "2001:db8::1",
		"https://panel.example.com":   "",
		"":                            "",
		"not a url":                   "",
	}
	for in, want := range cases {
		if got := panelAddrOf(in); got != want {
			t.Errorf("panelAddrOf(%q) = %q, want %q", in, got, want)
		}
	}
}
