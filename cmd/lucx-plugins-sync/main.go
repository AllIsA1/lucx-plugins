// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

// Command lucx-plugins-sync keeps a server's panel binary in step with the signed builds list.
// It runs before the panel starts (at-start), from a timer (check, apply) and when the plugin
// system is removed (restore).
package main

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/AllIsA1/lucx-plugins/internal/agent"
	"github.com/AllIsA1/lucx-plugins/internal/builds"
	"github.com/AllIsA1/lucx-plugins/internal/hostupdate"
	"github.com/AllIsA1/lucx-plugins/keys"
)

// version is set at build time.
var version = "dev"

// trustedKeys is a variable so tests can sign with their own key.
var trustedKeys = keys.Builds

const (
	defaultConfig  = "/etc/lucx-plugins/config"
	usage          = "usage: lucx-plugins-sync [-config <file>] [-index-url <url>] <at-start|check|apply|update [--no-restart]|restore|status|version>\n       lucx-plugins-sync [-host-exe <file>] [-host-state <dir>] <host-guard|host-install>\n"
	defaultPanel   = "/usr/local/x-ui/x-ui"
	defaultState   = "/var/lib/lucx-plugins"
	defaultService = "x-ui"
	defaultRestart = "04:00"

	defaultHostExe   = "/usr/local/lucx-plugins/lucx-plugin-host"
	defaultHostState = "/var/lib/lucx-plugin-host"
)

type config struct {
	PanelBinary string
	StateDir    string
	Service     string
	RestartAt   string
	IndexURL    string
}

func parseConfig(r io.Reader) (config, error) {
	cfg := config{PanelBinary: defaultPanel, StateDir: defaultState, Service: defaultService, RestartAt: defaultRestart}
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if !ok || value == "" {
			return cfg, fmt.Errorf("line %d: want KEY=value", n)
		}
		switch strings.TrimSpace(key) {
		case "PANEL_BINARY":
			cfg.PanelBinary = value
		case "STATE_DIR":
			cfg.StateDir = value
		case "SERVICE":
			cfg.Service = value
		case "RESTART_AT":
			cfg.RestartAt = value
		case "INDEX_URL":
			cfg.IndexURL = value
		default:
			return cfg, fmt.Errorf("line %d: unknown setting %q", n, strings.TrimSpace(key))
		}
	}
	return cfg, sc.Err()
}

// indexURL picks the builds list address: the flag, then LUCX_PLUGINS_INDEX_URL, then the configuration.
// An empty result means the agent's default (the published list).
func indexURL(cfg config, flagValue, env string) string {
	for _, v := range []string{flagValue, env, cfg.IndexURL} {
		if v != "" {
			return v
		}
	}
	return ""
}

func main() { os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr)) }

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("lucx-plugins-sync", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	confPath := fs.String("config", defaultConfig, "configuration file")
	indexFlag := fs.String("index-url", "", "builds list address (default: the published list)")
	hostExe := fs.String("host-exe", defaultHostExe, "host-guard: the plugin host binary")
	hostState := fs.String("host-state", defaultHostState, "host-guard: the plugin host's state directory")
	if err := fs.Parse(args); err != nil || fs.NArg() < 1 || fs.NArg() > 2 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	command := fs.Arg(0)
	noRestart := false
	if fs.NArg() == 2 {
		if command != "update" || (fs.Arg(1) != "--no-restart" && fs.Arg(1) != "-no-restart") {
			fmt.Fprint(stderr, usage)
			return 2
		}
		noRestart = true
	}
	if command == "version" {
		fmt.Fprintln(stdout, version)
		return 0
	}
	switch command {
	case "host-guard":
		return hostGuard(*hostExe, *hostState, stderr)
	case "host-install":
		return hostInstall(ctx, *hostExe, *hostState, stdout, stderr)
	}
	if !map[string]bool{"at-start": true, "check": true, "apply": true, "update": true, "restore": true, "status": true}[command] {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cfg := config{PanelBinary: defaultPanel, StateDir: defaultState, Service: defaultService, RestartAt: defaultRestart}
	f, err := os.Open(*confPath)
	switch {
	case err == nil:
		cfg, err = parseConfig(f)
		_ = f.Close()
		if err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", *confPath, err)
			return 2
		}
	case *confPath != defaultConfig || !errors.Is(err, os.ErrNotExist):
		fmt.Fprintf(stderr, "cannot read %s: %v\n", *confPath, err)
		return 2
	}
	var keyList []ed25519.PublicKey = trustedKeys()
	a := agent.New(agent.Config{
		PanelBinary: cfg.PanelBinary,
		StateDir:    cfg.StateDir,
		Arch:        runtime.GOARCH,
		IndexURL:    indexURL(cfg, *indexFlag, os.Getenv("LUCX_PLUGINS_INDEX_URL")),
		Keys:        keyList,
		Log:         func(format string, v ...any) { fmt.Fprintf(stderr, "lucx-plugins: "+format+"\n", v...) },
		Restart:     func(ctx context.Context) error { return restartService(ctx, cfg.Service) },
	})
	switch command {
	case "at-start":
		// It runs before the panel: whatever happens, the panel must still start.
		if err := a.AtStart(ctx); err != nil {
			fmt.Fprintf(stderr, "lucx-plugins: %v\n", err)
		}
		return 0
	case "check":
		return report(stderr, a.Check(ctx))
	case "apply":
		return report(stderr, a.Apply(ctx))
	case "update":
		return update(ctx, a, noRestart, cfg, stdout, stderr)
	case "restore":
		changed, err := a.Restore(ctx)
		if err != nil {
			return report(stderr, err)
		}
		if changed {
			fmt.Fprintln(stdout, "restored the original panel binary")
		} else {
			fmt.Fprintln(stdout, "nothing to restore: the panel binary is not our build")
		}
		return 0
	default: // status
		printStatus(stdout, a.Status(), cfg)
		return 0
	}
}

// restartService is a variable so tests do not restart a real service.
var restartService = func(ctx context.Context, service string) error {
	if out, err := exec.CommandContext(ctx, "systemctl", "restart", service).CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl restart %s: %w: %s", service, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// updater is what update needs of the agent.
type updater interface {
	Update(ctx context.Context, restart bool) (agent.UpdateResult, error)
}

// update is the manual way to move the panel to the newest signed build: the output says what was
// found and done, and a failure says why.
func update(ctx context.Context, a updater, noRestart bool, cfg config, stdout, stderr io.Writer) int {
	res, err := a.Update(ctx, !noRestart)
	if err != nil {
		fmt.Fprintf(stderr, "lucx-plugins: %v\n", err)
		return 1
	}
	name := func(release string) string {
		if release == "" {
			return "the upstream panel (no plugin build)"
		}
		return release
	}
	switch {
	case res.Changed:
		fmt.Fprintf(stdout, "updated the panel: %s -> %s\n", name(res.Before), res.After)
	case res.Waiting:
		fmt.Fprintf(stdout, "a newer build %s is ready (now: %s); it will be installed at the next panel start (automatic restart around %s, or run this command without --no-restart)\n", res.After, name(res.Before), cfg.RestartAt)
	default:
		fmt.Fprintf(stdout, "the panel is up to date: %s\n", res.After)
	}
	return 0
}

// hostGuard is the plugin host's pre-start step: it counts the starts of a freshly updated host and
// puts the previous binary back when the new one keeps failing. Whatever happens, the host still starts.
func hostGuard(exe, stateDir string, stderr io.Writer) int {
	rolledBack, err := hostupdate.Guard(hostupdate.Config{Exe: exe, StateDir: stateDir})
	switch {
	case err != nil:
		fmt.Fprintf(stderr, "lucx-plugins: %v\n", err)
	case rolledBack:
		fmt.Fprintln(stderr, "lucx-plugins: the updated plugin host did not start properly and was rolled back to the previous release")
	}
	return 0
}

// hostInstall puts the plugin host, the release the signed host.json names, where none is installed. The
// installer runs it. LUCX_HOST_UPDATE_BASE, LUCX_HOST_UPDATE_DOWNLOAD_BASE and LUCX_HOST_UPDATE_PUBKEYS
// (the same variables the host reads) point it at a mirror and a key of your own for development.
func hostInstall(ctx context.Context, exe, stateDir string, stdout, stderr io.Writer) int {
	if st, err := os.Stat(exe); err == nil && !st.IsDir() {
		fmt.Fprintf(stdout, "the plugin host is already installed (%s); updates go through the Plugins page\n", exe)
		return 0
	}
	keyList := trustedKeys()
	if env := os.Getenv("LUCX_HOST_UPDATE_PUBKEYS"); env != "" {
		parsed, err := builds.ParseKeys(strings.Split(env, ","))
		if err != nil {
			fmt.Fprintf(stderr, "lucx-plugins: LUCX_HOST_UPDATE_PUBKEYS: %v\n", err)
			return 2
		}
		keyList = parsed
	}
	cfg := hostupdate.Config{
		Exe: exe, StateDir: stateDir, Arch: runtime.GOARCH, Managed: true,
		BaseURL: os.Getenv("LUCX_HOST_UPDATE_BASE"), Keys: keyList,
		Log: func(format string, v ...any) { fmt.Fprintf(stderr, "lucx-plugins: "+format+"\n", v...) },
	}
	if base := os.Getenv("LUCX_HOST_UPDATE_DOWNLOAD_BASE"); base != "" {
		prefix := "https://github.com/" + builds.OwnRepo + "/releases/download/"
		cfg.Rewrite = func(u string) string { return strings.Replace(u, prefix, strings.TrimSuffix(base, "/")+"/", 1) }
	}
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		fmt.Fprintf(stderr, "lucx-plugins: %v\n", err)
		return 1
	}
	if err := hostupdate.New(cfg).InstallFresh(ctx); err != nil {
		fmt.Fprintf(stderr, "lucx-plugins: cannot install the plugin host: %v\n", err)
		return 1
	}
	out, err := exec.CommandContext(ctx, exe, "-v").Output()
	if err != nil {
		fmt.Fprintf(stderr, "lucx-plugins: the installed plugin host does not run: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "installed the plugin host %s at %s\n", strings.TrimSpace(string(out)), exe)
	return 0
}

func report(stderr io.Writer, err error) int {
	if err != nil {
		fmt.Fprintf(stderr, "lucx-plugins: %v\n", err)
		return 1
	}
	return 0
}

func printStatus(w io.Writer, st agent.State, cfg config) {
	fmt.Fprintf(w, "panel binary: %s\n", cfg.PanelBinary)
	if st.Ours == "" {
		fmt.Fprintln(w, "the panel binary is not a known build with the plugin system")
	} else {
		fmt.Fprintf(w, "plugin build installed for %s (%s) since %s\n", st.Upstream, st.Release, st.SwappedAt.Format("2006-01-02 15:04 MST"))
	}
	if st.Pending {
		fmt.Fprintf(w, "a plugin build is ready and will be installed at the next panel start (automatic restart around %s)\n", cfg.RestartAt)
	}
	if !st.CheckedAt.IsZero() {
		fmt.Fprintf(w, "last check: %s\n", st.CheckedAt.Format("2006-01-02 15:04 MST"))
	}
	if st.LastError != "" {
		fmt.Fprintf(w, "last problem: %s\n", st.LastError)
	}
}
