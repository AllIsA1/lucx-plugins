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
	"runtime"
	"strings"

	"github.com/AllIsA1/lucx-plugins/internal/agent"
	"github.com/AllIsA1/lucx-plugins/keys"
)

// version is set at build time.
var version = "dev"

// trustedKeys is a variable so tests can sign with their own key.
var trustedKeys = keys.Builds

const (
	defaultConfig  = "/etc/lucx-plugins/config"
	usage          = "usage: lucx-plugins-sync [-config <file>] [-index-url <url>] <at-start|check|apply|restore|status|version>\n"
	defaultPanel   = "/usr/local/x-ui/x-ui"
	defaultState   = "/var/lib/lucx-plugins"
	defaultService = "x-ui"
	defaultRestart = "04:00"
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
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	command := fs.Arg(0)
	if command == "version" {
		fmt.Fprintln(stdout, version)
		return 0
	}
	if !map[string]bool{"at-start": true, "check": true, "apply": true, "restore": true, "status": true}[command] {
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
		Restart: func(ctx context.Context) error {
			return exec.CommandContext(ctx, "systemctl", "restart", cfg.Service).Run()
		},
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
		fmt.Fprintf(w, "plugin build installed for %s since %s\n", st.Upstream, st.SwappedAt.Format("2006-01-02 15:04 MST"))
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
