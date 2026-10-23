// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

// Package hostlink is everything the panel knows about the plugin system: where the plugin host
// listens, what to forward to it (admin API, plugin pages, events, the Xray config) and the one
// door plugins use to call the panel's API. The panel never runs plugins itself; when the host is
// not running the panel is exactly the panel it would be without plugins.
package hostlink

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"
)

// IntegrationAPI is the version of the contract between this integration and the plugin host.
const IntegrationAPI = 1

const (
	defaultHostSocket     = "/run/lucx-plugin-host/host.sock"
	defaultInternalSocket = "/run/lucx-panel/internal.sock"

	// disabledEnv switches the whole integration off; the plugin routes then say so.
	disabledEnv = "XUI_PLUGINS_DISABLED"

	callTimeout     = 3 * time.Second
	eventQueueSize  = 256
	maxPipelineBody = 16 << 20
)

// How often the handshake runs: slowly while the host answers, quickly while it does not, so a
// restarted host gets its events again within seconds.
var (
	refreshInterval = 15 * time.Second
	retryInterval   = 2 * time.Second
)

// Config says where the two unix sockets are.
type Config struct {
	HostSocket     string // the plugin host's socket, which the panel calls
	InternalSocket string // the panel's internal API socket, which the host calls
	Disabled       bool
}

// DefaultConfig reads the optional XUI_PLUGIN_HOST_SOCKET and XUI_PLUGIN_INTERNAL_SOCKET overrides
// (used by development setups and tests) and the XUI_PLUGINS_DISABLED switch.
func DefaultConfig() Config {
	return Config{
		HostSocket:     envOr("XUI_PLUGIN_HOST_SOCKET", defaultHostSocket),
		InternalSocket: envOr("XUI_PLUGIN_INTERNAL_SOCKET", defaultInternalSocket),
		Disabled:       os.Getenv(disabledEnv) == "1",
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Status is what the panel knows about the host right now.
type Status struct {
	Disabled    bool
	Connected   bool
	Compatible  bool
	HostVersion string
	Reason      string // why the plugin system is not usable, empty when it is
}

type subscriptions struct {
	events    map[string]struct{}
	pipelines map[string]struct{}
}

// Link is one panel's connection to the plugin host.
type Link struct {
	cfg  Config
	call *http.Client // short calls: handshake, subscriptions, events, the Xray pipeline
	slow *http.Transport

	subs   atomic.Pointer[subscriptions]
	status atomic.Pointer[Status]
	lastOK atomic.Int64 // unix seconds of the last handshake that found a compatible host

	events chan []byte
	kick   chan struct{}

	mu           sync.Mutex
	stopInternal func()
	cancel       context.CancelFunc
	done         sync.WaitGroup
	warned       atomic.Int64 // unix seconds of the last "dropped" warning
}

// New builds a link; nothing runs until Start.
func New(cfg Config) *Link {
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: callTimeout}).DialContext(ctx, "unix", cfg.HostSocket)
	}
	l := &Link{
		cfg:    cfg,
		call:   &http.Client{Transport: &http.Transport{DialContext: dial, DisableKeepAlives: true}, Timeout: callTimeout},
		slow:   &http.Transport{DialContext: dial, ResponseHeaderTimeout: proxyTimeout, DisableKeepAlives: true},
		events: make(chan []byte, eventQueueSize),
		kick:   make(chan struct{}, 1),
	}
	l.setStatus(Status{Disabled: cfg.Disabled, Reason: notStartedReason(cfg)})
	l.subs.Store(&subscriptions{})
	return l
}

func notStartedReason(cfg Config) string {
	if cfg.Disabled {
		return "the plugin system is switched off (" + disabledEnv + "=1)"
	}
	return "the plugin system service is not running"
}

func (l *Link) setStatus(s Status) { l.status.Store(&s) }

// Status reports the last handshake.
func (l *Link) Status() Status { return *l.status.Load() }

// Start begins the handshake loop and the event sender. It never blocks and never fails: a missing
// host only means "no plugins".
func (l *Link) Start() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cancel != nil || l.cfg.Disabled {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	l.cancel = cancel
	l.done.Add(2)
	go l.refreshLoop(ctx)
	go l.sendLoop(ctx)
}

// ServeInternal opens the internal API socket in front of the panel's engine, so the host can call the
// panel on behalf of plugins. It does nothing when the integration is switched off.
func (l *Link) ServeInternal(engine http.Handler, basePath func() string) error {
	if l.cfg.Disabled {
		return nil
	}
	stop, err := ListenInternal(l.cfg.InternalSocket, InternalHandler(engine, basePath))
	if err != nil {
		return err
	}
	l.mu.Lock()
	previous := l.stopInternal
	l.stopInternal = stop
	l.mu.Unlock()
	if previous != nil {
		previous()
	}
	return nil
}

// Stop ends the loops and closes the internal socket. Queued events are dropped.
func (l *Link) Stop() {
	l.mu.Lock()
	cancel := l.cancel
	l.cancel = nil
	stopInternal := l.stopInternal
	l.stopInternal = nil
	l.mu.Unlock()
	if stopInternal != nil {
		stopInternal()
	}
	if cancel == nil {
		return
	}
	cancel()
	l.done.Wait()
	l.slow.CloseIdleConnections()
	l.subs.Store(&subscriptions{})
	l.lastOK.Store(0)
	l.setStatus(Status{Reason: notStartedReason(l.cfg)})
}

func (l *Link) getJSON(ctx context.Context, path string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://host"+path, nil)
	if err != nil {
		return err
	}
	resp, err := l.call.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %d", path, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(into)
}

// Refresh runs the handshake now and reloads what plugins subscribed to.
func (l *Link) Refresh(ctx context.Context) {
	var hello struct {
		IntegrationAPI int    `json:"integrationApi"`
		Host           string `json:"host"`
	}
	if err := l.getJSON(ctx, "/internal/hello", &hello); err != nil {
		l.subs.Store(&subscriptions{})
		l.setStatus(Status{Reason: "the plugin system service is not running"})
		return
	}
	if hello.IntegrationAPI != IntegrationAPI {
		l.subs.Store(&subscriptions{})
		l.setStatus(Status{
			Connected: true, HostVersion: hello.Host,
			Reason: fmt.Sprintf("the plugin system speaks integration API %d, this panel build speaks %d: update the plugin system or the panel build", hello.IntegrationAPI, IntegrationAPI),
		})
		return
	}
	var list struct {
		Events    []string `json:"events"`
		Pipelines []string `json:"pipelines"`
	}
	if err := l.getJSON(ctx, "/internal/subscriptions", &list); err != nil {
		l.subs.Store(&subscriptions{})
		l.setStatus(Status{Connected: true, Compatible: true, HostVersion: hello.Host, Reason: "the plugin system did not report its subscriptions"})
		return
	}
	s := &subscriptions{events: map[string]struct{}{}, pipelines: map[string]struct{}{}}
	for _, e := range list.Events {
		s.events[e] = struct{}{}
	}
	for _, p := range list.Pipelines {
		s.pipelines[p] = struct{}{}
	}
	l.subs.Store(s)
	l.lastOK.Store(time.Now().Unix())
	l.setStatus(Status{Connected: true, Compatible: true, HostVersion: hello.Host})
}

// availableGrace keeps a host that is only restarting (a self-update takes seconds) from looking gone.
const availableGrace = time.Minute

// Available reports whether a compatible host answered a moment ago.
func (l *Link) Available() bool {
	if l.Status().Compatible {
		return true
	}
	last := l.lastOK.Load()
	return last != 0 && time.Now().Unix()-last < int64(availableGrace/time.Second)
}

// Kick asks for a handshake soon; the admin proxy calls it after every change so a freshly enabled
// plugin gets its events without waiting for the next tick.
func (l *Link) Kick() {
	select {
	case l.kick <- struct{}{}:
	default:
	}
}

func (l *Link) refreshLoop(ctx context.Context) {
	defer l.done.Done()
	l.Refresh(ctx)
	for {
		wait := refreshInterval
		if !l.Status().Compatible {
			wait = retryInterval
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		case <-l.kick:
			timer.Stop()
			// Let the change that caused the kick finish first.
			select {
			case <-ctx.Done():
				return
			case <-time.After(300 * time.Millisecond):
			}
		}
		l.Refresh(ctx)
	}
}

// Wanted reports whether some enabled plugin subscribed to the event.
func (l *Link) Wanted(event string) bool {
	_, ok := l.subs.Load().events[event]
	return ok
}

// HasPipeline reports whether some enabled plugin holds the pipeline.
func (l *Link) HasPipeline(name string) bool {
	_, ok := l.subs.Load().pipelines[name]
	return ok
}

func warnRateLimited(last *atomic.Int64, format string, args ...any) {
	now := time.Now().Unix()
	if prev := last.Load(); now-prev >= 60 && last.CompareAndSwap(prev, now) {
		logger.Warningf(format, args...)
	}
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
