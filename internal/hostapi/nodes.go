// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/AllIsA1/lucx-plugins/internal/logx"
	"github.com/AllIsA1/lucx-plugins/internal/plugins"
)

// Per-node plugin states the UI renders; locked is a node whose owner has not allowed the master.
const (
	NodePluginsOK          = "ok"
	NodePluginsPending     = "pending"
	NodePluginsOffline     = "offline"
	NodePluginsUnsupported = "unsupported"
	NodePluginsError       = "error"
	NodePluginsLocked      = "locked"
)

const (
	nodePluginsConcurrency = 8
	nodePluginsTimeout     = 15 * time.Second
	nodePluginsSettleDelay = 3 * time.Second
	// NodePluginsPollEvery is how often the master re-reads its nodes.
	NodePluginsPollEvery = 30 * time.Second
	featurePlugins       = "plugins"
)

var (
	ErrNodeNotFound         = errors.New("node not found")
	ErrNodeUnsupported      = errors.New("this node does not support plugins")
	ErrNodeUnavailable      = errors.New("the node is disabled or offline")
	ErrPermissionsDiffer    = errors.New("the permissions differ from those granted on this panel")
	ErrNodeSlow             = errors.New("the node did not answer in time; it may still be working, check its status shortly")
	ErrPluginNotPinnable    = errors.New("this plugin has no recorded manifest hash: reinstall it on this panel first")
	ErrNodeUpToDate         = errors.New("the node already runs this panel's version of the plugin")
	ErrNotOnNode            = errors.New("the plugin is not installed on this node: install it instead")
	ErrNotManagedByMaster   = errors.New("the plugin was installed on the node by hand and is not managed by this panel")
	ErrNodeUpdateIncomplete = errors.New("the update on the node is incomplete")
)

// NodeInfo is what the master panel says about one of its nodes.
type NodeInfo struct {
	ID       int
	Name     string
	Enable   bool
	Status   string // online|offline|unknown
	Features []string
}

// NodePluginRuntime is the live state of a plugin process on a node.
type NodePluginRuntime struct {
	State          string `json:"state"`
	Healthy        bool   `json:"healthy"`
	PID            int    `json:"pid"`
	Restarts       int    `json:"restarts"`
	HealthError    string `json:"healthError,omitempty"`
	Detail         string `json:"detail,omitempty"`
	LastError      string `json:"lastError,omitempty"`
	EventsSent     int    `json:"eventsSent,omitempty"`
	EventsFailed   int    `json:"eventsFailed,omitempty"`
	EventsDropped  int    `json:"eventsDropped,omitempty"`
	PipelineErrors int    `json:"pipelineErrors,omitempty"`
}

// NodePlugin is one plugin installed on a node.
type NodePlugin struct {
	ID              string             `json:"id"`
	Name            string             `json:"name"`
	Version         string             `json:"version"`
	Type            string             `json:"type"`
	Scope           string             `json:"scope"`
	Commit          string             `json:"commit"`
	ManifestSHA256  string             `json:"manifestSha256,omitempty"`
	Enabled         bool               `json:"enabled"`
	DisabledReason  string             `json:"disabledReason,omitempty"`
	ManagedByMaster bool               `json:"managedByMaster,omitempty"`
	Permissions     []string           `json:"permissions"`
	Runtime         *NodePluginRuntime `json:"runtime,omitempty"`
}

// NodePluginsState is what a node reports about its plugin system.
type NodePluginsState struct {
	Available             bool         `json:"available"`
	AllowMasterManagement bool         `json:"allowMasterManagement"`
	ProcessesAvailable    bool         `json:"processesAvailable"`
	ProcessesError        string       `json:"processesError"`
	SafeMode              bool         `json:"safeMode"`
	Error                 string       `json:"error"`
	Plugins               []NodePlugin `json:"plugins"`
}

// NodePluginInstall is what a master approved: one commit, one manifest and exactly these
// permissions. The node downloads and verifies it itself.
type NodePluginInstall struct {
	URL            string   `json:"url"`
	Commit         string   `json:"commit"`
	ManifestSHA256 string   `json:"manifestSha256"`
	Permissions    []string `json:"permissions"`
}

// NodeRefusal is a node's own refusal of a plugin call, in the node's words.
type NodeRefusal struct {
	Status int
	Msg    string
}

func (e *NodeRefusal) Error() string { return e.Msg }

// NodePluginsClient is what the master can ask of one node's plugin system.
type NodePluginsClient interface {
	PluginsList(ctx context.Context) (*NodePluginsState, error)
	PluginsInstall(ctx context.Context, req NodePluginInstall) error
	// PluginsUpdate moves an installed plugin to the pinned commit in place.
	PluginsUpdate(ctx context.Context, req NodePluginInstall) error
	// SystemState is the node's own plugin-system state (the answer of its /plugins/system, passed on as it is).
	SystemState(ctx context.Context) (json.RawMessage, error)
	SystemCheck(ctx context.Context) error
	// SystemUpdate updates the node's plugin system; part "panel" updates the node's plugin build of the panel.
	SystemUpdate(ctx context.Context, part string) error
	PluginsSetEnabled(ctx context.Context, id string, enabled bool) error
	PluginsUninstall(ctx context.Context, id string, removeData bool) error
	PluginsRestart(ctx context.Context, id string) error
	PluginsLogs(ctx context.Context, id string, lines int) ([]string, error)
}

// NodePluginStatus is what the master knows about the plugins of one node.
type NodePluginStatus struct {
	NodeID    int               `json:"nodeId"`
	NodeName  string            `json:"nodeName"`
	State     string            `json:"state"`
	Error     string            `json:"error,omitempty"`
	FetchedAt int64             `json:"fetchedAt"`
	Plugins   *NodePluginsState `json:"plugins,omitempty"`
}

type nodePluginCache struct {
	mu sync.Mutex
	m  map[int]NodePluginStatus
}

func newNodePluginCache() *nodePluginCache { return &nodePluginCache{m: map[int]NodePluginStatus{}} }

func (c *nodePluginCache) get(id int) (NodePluginStatus, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.m[id]
	return s, ok
}

func (c *nodePluginCache) put(s NodePluginStatus) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[s.NodeID] = s
}

func (c *nodePluginCache) retain(ids map[int]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id := range c.m {
		if !ids[id] {
			delete(c.m, id)
		}
	}
}

func (c *nodePluginCache) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}

// NodePluginsService is the master side of plugin management on nodes: explicit commands, no
// convergence, and only the commit this panel itself has installed.
type NodePluginsService struct {
	Entries func() ([]plugins.Entry, error)
	// Nodes lists the master panel's nodes.
	Nodes func(ctx context.Context) ([]NodeInfo, error)
	// Client reaches one node's plugin system.
	Client func(NodeInfo) NodePluginsClient
	// SettleDelay re-reads a node once more after a command: its process stops or starts a moment later.
	SettleDelay time.Duration
	cache       *nodePluginCache
}

// NewNodePluginsServiceFor builds a service over the given registry, node list and node clients.
func NewNodePluginsServiceFor(entries func() ([]plugins.Entry, error), nodes func(context.Context) ([]NodeInfo, error), client func(NodeInfo) NodePluginsClient) *NodePluginsService {
	return &NodePluginsService{Entries: entries, Nodes: nodes, Client: client, cache: newNodePluginCache(), SettleDelay: nodePluginsSettleDelay}
}

func supportsPlugins(n NodeInfo) bool { return slices.Contains(n.Features, featurePlugins) }

// Statuses lists every enabled node with the last plugin state the master read from it.
func (s *NodePluginsService) Statuses(ctx context.Context) ([]NodePluginStatus, error) {
	nodes, err := s.Nodes(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]NodePluginStatus, 0, len(nodes))
	for _, n := range nodes {
		if !n.Enable {
			continue
		}
		st := NodePluginStatus{NodeID: n.ID, NodeName: n.Name, State: NodePluginsPending}
		switch cached, ok := s.cache.get(n.ID); {
		case !supportsPlugins(n):
			st.State = NodePluginsUnsupported
		case n.Status != "online":
			st.State = NodePluginsOffline
		case ok:
			st = cached
			st.NodeName = n.Name
		}
		out = append(out, st)
	}
	return out, nil
}

// Poll re-reads the plugin state of every reachable node that supports plugins.
func (s *NodePluginsService) Poll(ctx context.Context) {
	nodes, err := s.Nodes(ctx)
	if err != nil {
		return
	}
	known := make(map[int]bool, len(nodes))
	sem := make(chan struct{}, nodePluginsConcurrency)
	var wg sync.WaitGroup
	for _, n := range nodes {
		known[n.ID] = true
		if !n.Enable || n.Status != "online" || !supportsPlugins(n) {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					logx.Warningf("plugins: reading node %s panicked: %v", n.Name, r)
				}
			}()
			s.refresh(ctx, n)
		}()
	}
	wg.Wait()
	s.cache.retain(known)
}

// Run polls every NodePluginsPollEvery until ctx ends.
func (s *NodePluginsService) Run(ctx context.Context) {
	t := time.NewTicker(NodePluginsPollEvery)
	defer t.Stop()
	for {
		s.Poll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *NodePluginsService) refresh(ctx context.Context, n NodeInfo) {
	st := NodePluginStatus{NodeID: n.ID, NodeName: n.Name, State: NodePluginsOK, FetchedAt: time.Now().Unix()}
	rctx, cancel := context.WithTimeout(ctx, nodePluginsTimeout)
	var err error
	st.Plugins, err = s.Client(n).PluginsList(rctx)
	cancel()
	if err != nil {
		st.State, st.Error = NodePluginsError, err.Error()
		var refusal *NodeRefusal
		if errors.As(err, &refusal) && refusal.Status == http.StatusForbidden {
			st.State = NodePluginsLocked
		}
	}
	s.cache.put(st)
}

// target resolves a node that can take a command right now.
func (s *NodePluginsService) target(ctx context.Context, nodeID int) (NodeInfo, NodePluginsClient, error) {
	nodes, err := s.Nodes(ctx)
	if err != nil {
		return NodeInfo{}, nil, err
	}
	i := slices.IndexFunc(nodes, func(n NodeInfo) bool { return n.ID == nodeID })
	if i < 0 {
		return NodeInfo{}, nil, ErrNodeNotFound
	}
	n := nodes[i]
	switch {
	case !supportsPlugins(n):
		return NodeInfo{}, nil, ErrNodeUnsupported
	case !n.Enable || n.Status != "online":
		return NodeInfo{}, nil, ErrNodeUnavailable
	}
	return n, s.Client(n), nil
}

// pinned builds the node request from this panel's own registry, never from the caller's input.
func (s *NodePluginsService) pinned(pluginID string, permissions []string) (NodePluginInstall, error) {
	entries, err := s.Entries()
	if err != nil {
		return NodePluginInstall{}, err
	}
	for _, e := range entries {
		if e.ID != pluginID {
			continue
		}
		switch {
		case e.Scope != plugins.ScopeNode && e.Scope != plugins.ScopeBoth:
			return NodePluginInstall{}, fmt.Errorf("%w: scope is %q", plugins.ErrScopeNotAllowed, e.Scope)
		case e.Commit == "" || e.ManifestSHA256 == "":
			return NodePluginInstall{}, ErrPluginNotPinnable
		case !plugins.SamePermissionSet(e.Permissions, permissions):
			return NodePluginInstall{}, ErrPermissionsDiffer
		}
		src := e.Source
		src.Ref = ""
		return NodePluginInstall{
			URL: src.String(), Commit: e.Commit, ManifestSHA256: e.ManifestSHA256, Permissions: e.Permissions,
		}, nil
	}
	return NodePluginInstall{}, plugins.ErrNotFound
}

// command runs one node call and re-reads the node, so the UI sees the outcome at once.
func (s *NodePluginsService) command(ctx context.Context, nodeID int, run func(context.Context, NodePluginsClient) error) error {
	n, client, err := s.target(ctx, nodeID)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, nodePluginsTimeout)
	defer cancel()
	runErr := run(cctx, client)
	s.finish(ctx, n)
	return slowOr(runErr)
}

// finish re-reads the node now and once more after the settle delay: a process stops or starts a moment later.
func (s *NodePluginsService) finish(ctx context.Context, n NodeInfo) {
	s.refresh(ctx, n)
	if s.SettleDelay > 0 {
		time.AfterFunc(s.SettleDelay, func() {
			rctx, rcancel := context.WithTimeout(context.Background(), nodePluginsTimeout)
			defer rcancel()
			s.refresh(rctx, n)
		})
	}
}

// slowOr turns a timeout into ErrNodeSlow: the node may still be working.
func slowOr(err error) error {
	var timeout interface{ Timeout() bool }
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()) {
		return ErrNodeSlow
	}
	return err
}

// Update moves a node's plugin to this panel's commit by removing it (its data stays) and installing the
// pinned one, then turning it on again if it was on. There is no automatic way back: a failure says
// which state the node was left in.
func (s *NodePluginsService) Update(ctx context.Context, nodeID int, pluginID string, permissions []string) error {
	req, err := s.pinned(pluginID, permissions)
	if err != nil {
		return err
	}
	n, client, err := s.target(ctx, nodeID)
	if err != nil {
		return err
	}
	step := func(run func(context.Context) error) error {
		sctx, cancel := context.WithTimeout(ctx, nodePluginsTimeout)
		defer cancel()
		return slowOr(run(sctx))
	}
	var current *NodePlugin
	if err := step(func(c context.Context) error {
		st, err := client.PluginsList(c)
		if err != nil {
			return err
		}
		for i := range st.Plugins {
			if st.Plugins[i].ID == pluginID {
				current = &st.Plugins[i]
			}
		}
		return nil
	}); err != nil {
		return err
	}
	switch {
	case current == nil:
		return ErrNotOnNode
	case !current.ManagedByMaster:
		return ErrNotManagedByMaster
	case current.Commit == req.Commit:
		return ErrNodeUpToDate
	}
	wasEnabled := current.Enabled
	defer s.finish(ctx, n)

	// In place: the plugin is not removed first, which a plugin that others require would not allow. A node whose
	// plugin system does not have the route yet is updated the old way (remove, install, turn on).
	err = step(func(c context.Context) error { return client.PluginsUpdate(c, req) })
	if err == nil || !routeMissing(err) {
		return err
	}
	if err := step(func(c context.Context) error { return client.PluginsUninstall(c, pluginID, false) }); err != nil {
		return err
	}
	if err := step(func(c context.Context) error { return client.PluginsInstall(c, req) }); err != nil {
		return fmt.Errorf("%w: the old version was removed from the node and the new one could not be installed: %w", ErrNodeUpdateIncomplete, err)
	}
	if wasEnabled {
		if err := step(func(c context.Context) error { return client.PluginsSetEnabled(c, pluginID, true) }); err != nil {
			return fmt.Errorf("%w: the new version is installed but could not be turned on again: %w", ErrNodeUpdateIncomplete, err)
		}
	}
	return nil
}

// routeMissing says whether a node refused a call because its plugin system does not have that route (yet).
func routeMissing(err error) bool {
	var refusal *NodeRefusal
	if !errors.As(err, &refusal) {
		return false
	}
	return refusal.Status == http.StatusNotFound ||
		(refusal.Status == http.StatusForbidden && strings.Contains(refusal.Msg, "not available to a master panel"))
}

// System reads the plugin-system state of a node.
func (s *NodePluginsService) System(ctx context.Context, nodeID int) (json.RawMessage, error) {
	_, client, err := s.target(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, nodePluginsTimeout)
	defer cancel()
	obj, err := client.SystemState(cctx)
	return obj, slowOr(err)
}

// SystemCheck makes a node look for a newer plugin system and plugin build of its panel.
func (s *NodePluginsService) SystemCheck(ctx context.Context, nodeID int) error {
	_, client, err := s.target(ctx, nodeID)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, nodePluginsTimeout)
	defer cancel()
	return slowOr(client.SystemCheck(cctx))
}

// SystemUpdate updates a node's plugin system, or with part "panel" the node's plugin build of the panel. The node
// restarts its service (or its panel) a moment after it answers, so a call that is cut off by that is not a failure:
// the caller reads the node's state to see the outcome. The node's refusals are reported as they are.
func (s *NodePluginsService) SystemUpdate(ctx context.Context, nodeID int, part string) error {
	if part != "" && part != "panel" {
		return fmt.Errorf("unknown part %q", part)
	}
	n, client, err := s.target(ctx, nodeID)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, nodePluginsTimeout)
	defer cancel()
	err = client.SystemUpdate(cctx, part)
	if err == nil {
		return nil
	}
	var refusal *NodeRefusal
	if errors.As(err, &refusal) {
		return err
	}
	if part == "panel" {
		// The panel went away under the call, as it does when it restarts onto the new build.
		s.refresh(ctx, n)
		return nil
	}
	return slowOr(err)
}

// Refresh re-reads one node at once instead of waiting for the next poll.
func (s *NodePluginsService) Refresh(ctx context.Context, nodeID int) error {
	n, _, err := s.target(ctx, nodeID)
	if err != nil {
		return err
	}
	s.refresh(ctx, n)
	return nil
}

// Install puts the plugin this panel has installed onto a node, disabled.
func (s *NodePluginsService) Install(ctx context.Context, nodeID int, pluginID string, permissions []string) error {
	req, err := s.pinned(pluginID, permissions)
	if err != nil {
		return err
	}
	return s.command(ctx, nodeID, func(ctx context.Context, c NodePluginsClient) error {
		return c.PluginsInstall(ctx, req)
	})
}

// InstallPinned puts a plugin that is not installed on this panel onto a node, disabled: the request comes from a
// preview this panel made, so the node installs exactly the commit and manifest the operator saw.
func (s *NodePluginsService) InstallPinned(ctx context.Context, nodeID int, req NodePluginInstall) error {
	return s.command(ctx, nodeID, func(ctx context.Context, c NodePluginsClient) error {
		return c.PluginsInstall(ctx, req)
	})
}

// SetEnabled turns a node's plugin on or off.
func (s *NodePluginsService) SetEnabled(ctx context.Context, nodeID int, pluginID string, enabled bool) error {
	return s.command(ctx, nodeID, func(ctx context.Context, c NodePluginsClient) error {
		return c.PluginsSetEnabled(ctx, pluginID, enabled)
	})
}

// Restart restarts a node's plugin process.
func (s *NodePluginsService) Restart(ctx context.Context, nodeID int, pluginID string) error {
	return s.command(ctx, nodeID, func(ctx context.Context, c NodePluginsClient) error {
		return c.PluginsRestart(ctx, pluginID)
	})
}

// Uninstall removes a plugin from a node, optionally with its data.
func (s *NodePluginsService) Uninstall(ctx context.Context, nodeID int, pluginID string, removeData bool) error {
	return s.command(ctx, nodeID, func(ctx context.Context, c NodePluginsClient) error {
		return c.PluginsUninstall(ctx, pluginID, removeData)
	})
}

// Logs returns the last lines a node's plugin process wrote.
func (s *NodePluginsService) Logs(ctx context.Context, nodeID int, pluginID string, lines int) ([]string, error) {
	_, client, err := s.target(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, nodePluginsTimeout)
	defer cancel()
	out, err := client.PluginsLogs(cctx, pluginID, lines)
	if out == nil {
		out = []string{}
	}
	return out, err
}
