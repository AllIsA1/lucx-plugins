// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

// Command lucx-plugin-host is the plugin system as a service of its own: it installs and runs
// plugins and answers the panel integration on a unix socket. It is also the launcher that gives a
// plugin its own user (`lucx-plugin-host plugin-exec ...`).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/AllIsA1/lucx-plugins/internal/hostapi"
	"github.com/AllIsA1/lucx-plugins/internal/logx"
	"github.com/AllIsA1/lucx-plugins/internal/plugins"
)

// version is set at build time.
var version = "dev"

type config struct {
	StateDir    string
	Socket      string
	Gateway     string
	PanelSocket string
	Version     string
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
	flag.StringVar(&cfg.PanelSocket, "panel-socket", envOr("LUCX_HOST_PANEL_SOCKET", "/run/lucx-plugin-host/panel.sock"), "the panel's internal API socket")
	showVersion := flag.Bool("v", false, "print the version")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cfg); err != nil {
		logx.Error("lucx-plugin-host:", err)
		os.Exit(1)
	}
}

// run serves until ctx ends. Plugin processes started by systemd keep running when the host stops.
func run(ctx context.Context, cfg config) error {
	plugins.SetPanelURL(func() string { return "http://" + cfg.Gateway })
	plugins.Start(plugins.DefaultConfig(cfg.StateDir))
	defer plugins.Stop()

	svc := hostapi.NewPluginService()
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
	select {
	case <-ctx.Done():
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
	return nil
}
