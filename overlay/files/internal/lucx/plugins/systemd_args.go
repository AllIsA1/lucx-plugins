// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import (
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

const (
	unitPrefix   = "lucx-plugin-"
	unitSuffix   = ".service"
	descPrefix   = "lucx-plugin:"
	systemdFirst = 239
)

// systemdOptions adapts the unit to where it runs. User is the unprivileged
// user manager used by tests: DynamicUser and IP filtering need the system one.
type systemdOptions struct {
	User             bool
	SystemCallFilter bool
	RestartSec       string
}

func unitName(id string) string { return unitPrefix + id + unitSuffix }

func idFromUnit(unit string) (string, bool) {
	if !strings.HasPrefix(unit, unitPrefix) || !strings.HasSuffix(unit, unitSuffix) {
		return "", false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(unit, unitPrefix), unitSuffix)
	return id, idPattern.MatchString(id)
}

func unitDescription(id, fingerprint string) string {
	return descPrefix + id + ":" + fingerprint
}

func fingerprintFromDescription(id, desc string) string {
	return strings.TrimPrefix(desc, descPrefix+id+":")
}

func hasPermission(perms []string, want string) bool {
	for _, p := range perms {
		if p == want {
			return true
		}
	}
	return false
}

// systemdProperties renders the sandbox of one plugin unit as Key=Value pairs.
func systemdProperties(spec ProcessSpec, opt systemdOptions) []string {
	limits := spec.Limits
	if limits == (ResourceLimits{}) {
		limits = DefaultResourceLimits
	}
	restartSec := opt.RestartSec
	if restartSec == "" {
		restartSec = "5"
	}
	props := []string{
		"Type=exec",
		"NoNewPrivileges=yes",
		"CapabilityBoundingSet=",
		"ProtectSystem=strict",
		"PrivateTmp=yes",
		"PrivateDevices=yes",
		"ProtectKernelTunables=yes",
		"ProtectKernelModules=yes",
		"ProtectKernelLogs=yes",
		"ProtectControlGroups=yes",
		"ProtectClock=yes",
		"ProtectHostname=yes",
		"RestrictNamespaces=yes",
		"RestrictRealtime=yes",
		"RestrictSUIDSGID=yes",
		"LockPersonality=yes",
		"RemoveIPC=yes",
		"UMask=0077",
		"RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6",
		"SystemCallArchitectures=native",
		"StateDirectory=x-ui-plugins/" + spec.ID,
		"RuntimeDirectory=x-ui-plugins/" + spec.ID,
		"WorkingDirectory=" + spec.CodeDir,
		"Restart=on-failure",
		"RestartSec=" + restartSec,
		"StartLimitIntervalSec=300",
		"StartLimitBurst=5",
		"TimeoutStopSec=10",
		"KillMode=control-group",
		"MemoryMax=" + limits.MemoryMax,
		"TasksMax=" + strconv.Itoa(limits.TasksMax),
		"CPUQuota=" + strconv.Itoa(limits.CPUQuotaPercent) + "%",
		"LimitCORE=0",
		"LimitNOFILE=4096",
	}
	if opt.User {
		props = append(props, "ProtectHome=read-only")
	} else {
		props = append(props,
			"ProtectHome=yes",
			"ProtectProc=invisible",
			"ProcSubset=pid",
			"DynamicUser=yes",
		)
		if !hasPermission(spec.Permissions, permNetwork) {
			props = append(props, "IPAddressDeny=any", "IPAddressAllow=localhost")
			if spec.PanelAddr != "" {
				props = append(props, "IPAddressAllow="+spec.PanelAddr)
			}
		}
	}
	if spec.TokenFile != "" {
		props = append(props, "LoadCredential="+tokenFileName+":"+spec.TokenFile)
	}
	if opt.SystemCallFilter && !opt.User {
		props = append(props, "SystemCallFilter=@system-service", "SystemCallErrorNumber=EPERM")
	}
	return props
}

// systemdRunArgs builds the argv of systemd-run; nothing goes through a shell.
// No --collect: a unit that hit its start limit must stay loaded as failed so
// the supervisor can see the crash loop instead of silently starting it anew.
func systemdRunArgs(spec ProcessSpec, opt systemdOptions) []string {
	args := []string{"--quiet", "--unit=" + unitName(spec.ID), "--description=" + unitDescription(spec.ID, spec.Fingerprint)}
	if opt.User {
		args = append([]string{"--user"}, args...)
	}
	for _, p := range systemdProperties(spec, opt) {
		args = append(args, "--property="+p)
	}
	env := pluginEnv(spec)
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--setenv="+k+"="+env[k])
	}
	args = append(args, "--", spec.Command)
	return append(args, spec.Args...)
}

// pluginEnv is the environment every runner gives a plugin. Reserved variables
// are set last so a plugin spec cannot spoof them.
func pluginEnv(spec ProcessSpec) map[string]string {
	env := map[string]string{}
	for k, v := range spec.Env {
		env[k] = v
	}
	env["LUCX_PLUGIN_ID"] = spec.ID
	env["LUCX_API_VERSION"] = SupportedAPI
	env["LUCX_SOCKET"] = spec.SocketPath
	env["LUCX_DATA_DIR"] = spec.DataDir
	if spec.PanelURL != "" {
		env["LUCX_PANEL_URL"] = spec.PanelURL
	} else {
		delete(env, "LUCX_PANEL_URL")
	}
	return env
}

// parseSystemdVersion reads the major version from `systemctl --version`.
func parseSystemdVersion(out string) (int, error) {
	fields := strings.Fields(strings.SplitN(out, "\n", 2)[0])
	if len(fields) < 2 || fields[0] != "systemd" {
		return 0, fmt.Errorf("unexpected systemctl --version output %q", out)
	}
	return strconv.Atoi(fields[1])
}

// parseShowBlocks splits `systemctl show a b` output (blank-line separated
// Key=Value blocks) into one map per unit.
func parseShowBlocks(out string) []map[string]string {
	var blocks []map[string]string
	cur := map[string]string{}
	flush := func() {
		if len(cur) > 0 {
			blocks = append(blocks, cur)
			cur = map[string]string{}
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			cur[k] = v
		}
	}
	flush()
	return blocks
}

// statusFromShow maps systemctl properties to a RunStatus.
func statusFromShow(id string, kv map[string]string) RunStatus {
	st := RunStatus{ID: id, Fingerprint: fingerprintFromDescription(id, kv["Description"])}
	st.PID, _ = strconv.Atoi(kv["MainPID"])
	st.Restarts, _ = strconv.Atoi(kv["NRestarts"])
	switch kv["ActiveState"] {
	case "active":
		st.State = StateRunning
	case "activating", "reloading":
		st.State = StateStarting
	case "deactivating":
		st.State = StateStopping
	case "failed":
		st.State = StateFailed
		st.Detail = kv["Result"]
	default:
		st.State = StateStopped
	}
	if !strings.HasPrefix(kv["Description"], descPrefix+id+":") {
		st.Fingerprint = ""
	}
	return st
}

// parseListedIDs extracts plugin ids from `systemctl list-units --plain --no-legend`.
func parseListedIDs(out string) []string {
	var ids []string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(strings.TrimLeft(line, "●* "))
		if len(fields) == 0 {
			continue
		}
		if id, ok := idFromUnit(fields[0]); ok {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// panelAddrOf returns the panel's host when it is a non-loopback IP the plugin
// must be allowed to reach; names and loopback addresses need no extra rule.
func panelAddrOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || ip.IsLoopback() {
		return ""
	}
	return ip.String()
}
