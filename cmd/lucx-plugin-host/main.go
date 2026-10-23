// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

// Command lucx-plugin-host is the plugin system as a service of its own: it installs and runs
// plugins and answers the panel integration on a unix socket. It is also the launcher that gives a
// plugin its own user (`lucx-plugin-host plugin-exec ...`).
package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/AllIsA1/lucx-plugins/internal/builds"
	"github.com/AllIsA1/lucx-plugins/internal/hostapi"
	"github.com/AllIsA1/lucx-plugins/internal/hostupdate"
	"github.com/AllIsA1/lucx-plugins/internal/logx"
	"github.com/AllIsA1/lucx-plugins/internal/plugins"
	"github.com/AllIsA1/lucx-plugins/keys"
)

// version is set at build time.
var version = "dev"

type config struct {
	StateDir    string
	Socket      string
	Gateway     string
	PanelSocket string
	Version     string

	// The host replaces its own binary Exe with the build the signed list at UpdateBase names. Without
	// an Exe, or for a version that is not a release (host-N) and no explicit UpdateBase, it does not.
	Exe          string
	UpdateBase   string
	UpdateKeys   []ed25519.PublicKey // default: the keys builds.json is verified with
	DownloadBase string              // replaces https://github.com/AllIsA1/lucx-plugins/releases/download/ (tests, mirrors)
	Exec         func(path string, args, env []string) error
}

// errRestart is what run returns after a self-update asked for a restart.
var errRestart = errors.New("restart requested")

const releaseDownloadBase = "https://github.com/" + builds.OwnRepo + "/releases/download/"

func newUpdater(cfg config) *hostupdate.Updater {
	_, release := builds.HostNumber(cfg.Version)
	uc := hostupdate.Config{
		Exe: cfg.Exe, StateDir: cfg.StateDir, Version: cfg.Version, Arch: runtime.GOARCH,
		Managed: cfg.Exe != "" && (release || cfg.UpdateBase != ""),
		BaseURL: cfg.UpdateBase, Keys: cfg.UpdateKeys, Exec: cfg.Exec,
		Log: func(format string, args ...any) { logx.Infof(format, args...) },
	}
	if len(uc.Keys) == 0 {
		uc.Keys = keys.Builds()
	}
	if cfg.DownloadBase != "" {
		base := strings.TrimSuffix(cfg.DownloadBase, "/") + "/"
		uc.Rewrite = func(u string) string { return strings.Replace(u, releaseDownloadBase, base, 1) }
	}
	return hostupdate.New(uc)
}

// updateKeysFromEnv reads LUCX_HOST_UPDATE_PUBKEYS, base64 ed25519 keys separated by commas. It exists so a
// development setup can sign its own list; whoever sets it already controls this process.
func updateKeysFromEnv() ([]ed25519.PublicKey, error) {
	list := os.Getenv("LUCX_HOST_UPDATE_PUBKEYS")
	if list == "" {
		return nil, nil
	}
	return builds.ParseKeys(strings.Split(list, ","))
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	// The process runner re-executes this binary to confine a plugin before it starts.
	if len(os.Args) > 1 && os.Args[1] == plugins.ExecCommand {
		os.Exit(plugins.RunExec(os.Args[2:]))
	}
	cfg := config{Version: version}
	flag.StringVar(&cfg.StateDir, "state-dir", envOr("LUCX_HOST_STATE_DIR", "/var/lib/lucx-plugin-host"), "registry, keys and plugin code")
	flag.StringVar(&cfg.Socket, "socket", envOr("LUCX_HOST_SOCKET", "/run/lucx-plugin-host/host.sock"), "unix socket the panel talks to")
	flag.StringVar(&cfg.Gateway, "gateway", envOr("LUCX_HOST_GATEWAY", "127.0.0.1:19480"), "loopback address plugins call the panel API on")
	flag.StringVar(&cfg.PanelSocket, "panel-socket", envOr("LUCX_HOST_PANEL_SOCKET", "/run/lucx-panel/internal.sock"), "the panel's internal API socket")
	showVersion := flag.Bool("v", false, "print the version")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	cfg.UpdateBase = os.Getenv("LUCX_HOST_UPDATE_BASE")
	cfg.DownloadBase = os.Getenv("LUCX_HOST_UPDATE_DOWNLOAD_BASE")
	var err error
	if cfg.UpdateKeys, err = updateKeysFromEnv(); err != nil {
		logx.Error("lucx-plugin-host: LUCX_HOST_UPDATE_PUBKEYS:", err)
		os.Exit(1)
	}
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		cfg.Exe = exe
	}
	up := newUpdater(cfg)
	// Before anything else: a freshly installed binary that keeps failing gives way to the previous one.
	if _, err := up.AtStart(); err != nil {
		logx.Error("lucx-plugin-host:", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err = run(ctx, cfg, up)
	if errors.Is(err, errRestart) {
		logx.Infof("restarting as %s", cfg.Exe)
		if err = up.Restart(); err != nil {
			logx.Error("lucx-plugin-host: cannot restart:", err)
		}
	}
	if err != nil {
		logx.Error("lucx-plugin-host:", err)
		os.Exit(1)
	}
}

// run serves until ctx ends. Plugin processes started by systemd keep running when the host stops.
func run(ctx context.Context, cfg config, up *hostupdate.Updater) error {
	plugins.SetPanelURL(func() string { return "http://" + cfg.Gateway })
	plugins.Start(plugins.DefaultConfig(cfg.StateDir))
	defer plugins.Stop()

	svc := hostapi.NewPluginService()
	restart := make(chan struct{}, 1)
	svc.Updater = up
	svc.RestartHost = func() {
		select {
		case restart <- struct{}{}:
		default:
		}
	}
	srv := hostapi.NewServer(svc)
	srv.SetHooks(hostapi.DefaultHooks(svc, cfg.Version))
	hostLn, err := hostapi.Listen(cfg.Socket)
	if err != nil {
		return fmt.Errorf("cannot listen on %s: %w", cfg.Socket, err)
	}
	gwLn, err := net.Listen("tcp", cfg.Gateway)
	if err != nil {
		_ = hostLn.Close()
		return fmt.Errorf("cannot listen on %s: %w", cfg.Gateway, err)
	}
	servers := []*http.Server{
		{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second},
		{Handler: hostapi.NewGateway(svc, cfg.PanelSocket).Handler(), ReadHeaderTimeout: 10 * time.Second},
	}
	errs := make(chan error, 2)
	for i, ln := range []net.Listener{hostLn, gwLn} {
		go func() { errs <- servers[i].Serve(ln) }()
	}
	logx.Infof("lucx-plugin-host %s: socket %s, gateway %s", cfg.Version, cfg.Socket, cfg.Gateway)
	go up.Watch(ctx, time.Minute)
	go confirmWhenHealthy(ctx, cfg.Socket, up)
	var result error
	select {
	case <-ctx.Done():
	case <-restart:
		result = errRestart
	case err := <-errs:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, s := range servers {
		_ = s.Shutdown(shutdown)
	}
	return result
}

// confirmWhenHealthy tells the updater that this binary has been up and answering for long enough
// that it need not be rolled back.
func confirmWhenHealthy(ctx context.Context, socket string, up *hostupdate.Updater) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(up.ConfirmAfter()):
	}
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	resp, err := client.Get("http://host/internal/hello")
	if err != nil {
		logx.Warningf("the host does not answer on its own socket, not confirming this start: %v", err)
		return
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		up.Confirm()
	}
}
