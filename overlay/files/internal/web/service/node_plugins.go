// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/lucx/nodetype"
	"github.com/mhsanaei/3x-ui/v3/internal/lucx/plugins"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
	"github.com/mhsanaei/3x-ui/v3/internal/web/runtime"
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

// NodePluginsClient is the part of runtime.Remote the plugin commands use.
type NodePluginsClient interface {
	PluginsList(ctx context.Context) (*runtime.RemotePluginsState, error)
	PluginsInstall(ctx context.Context, req runtime.RemotePluginInstall) error
	PluginsSetEnabled(ctx context.Context, id string, enabled bool) error
	PluginsUninstall(ctx context.Context, id string, removeData bool) error
	PluginsRestart(ctx context.Context, id string) error
	PluginsLogs(ctx context.Context, id string, lines int) ([]string, error)
}

// NodePluginStatus is what the master knows about the plugins of one node.
type NodePluginStatus struct {
	NodeID    int                         `json:"nodeId"`
	NodeName  string                      `json:"nodeName"`
	State     string                      `json:"state"`
	Error     string                      `json:"error,omitempty"`
	FetchedAt int64                       `json:"fetchedAt"`
	Plugins   *runtime.RemotePluginsState `json:"plugins,omitempty"`
}

type nodePluginCache struct {
	mu sync.Mutex
	m  map[int]NodePluginStatus
}

func newNodePluginCache() *nodePluginCache {
	return &nodePluginCache{m: map[int]NodePluginStatus{}}
}

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

// The cache is process-wide so the polling job and the API see the same answers;
// it lives in memory only and refills within one polling period after a restart.
var sharedNodePluginCache = newNodePluginCache()

// NodePluginsService is the master side of plugin management on nodes: explicit commands,
// no convergence, and only the commit this panel itself has installed.
type NodePluginsService struct {
	Entries func() ([]plugins.Entry, error)
	Client  func(*model.Node) (NodePluginsClient, error)
	// SettleDelay re-reads a node once more after a command: its process stops or starts a moment later.
	SettleDelay time.Duration
	cache       *nodePluginCache
}

// NewNodePluginsService wires the service to the panel's plugin registry and node runtimes.
func NewNodePluginsService() *NodePluginsService {
	svc := NewNodePluginsServiceFor(
		func() ([]plugins.Entry, error) {
			m, err := plugins.Get()
			if err != nil {
				return nil, err
			}
			return m.List(), nil
		},
		func(n *model.Node) (NodePluginsClient, error) {
			mgr := runtime.GetManager()
			if mgr == nil {
				return nil, errors.New("runtime manager unavailable")
			}
			return mgr.RemoteFor(n)
		},
	)
	svc.cache = sharedNodePluginCache
	svc.SettleDelay = nodePluginsSettleDelay
	return svc
}

// NewNodePluginsServiceFor builds a service over the given registry and node clients, with its own cache.
func NewNodePluginsServiceFor(entries func() ([]plugins.Entry, error), client func(*model.Node) (NodePluginsClient, error)) *NodePluginsService {
	return &NodePluginsService{Entries: entries, Client: client, cache: newNodePluginCache()}
}

func supportsPlugins(n *model.Node) bool {
	return nodetype.FromJSON(n.Features).HasFeature(nodetype.FeaturePlugins)
}

func (s *NodePluginsService) loadNodes() ([]*model.Node, error) {
	return (&NodeService{}).GetAll()
}

// Statuses lists every enabled node with the last plugin state the master read from it.
func (s *NodePluginsService) Statuses() ([]NodePluginStatus, error) {
	nodes, err := s.loadNodes()
	if err != nil {
		return nil, err
	}
	out := make([]NodePluginStatus, 0, len(nodes))
	for _, n := range nodes {
		if !n.Enable {
			continue
		}
		st := NodePluginStatus{NodeID: n.Id, NodeName: n.Name, State: NodePluginsPending}
		switch cached, ok := s.cache.get(n.Id); {
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
	nodes, err := s.loadNodes()
	if err != nil {
		return
	}
	known := make(map[int]bool, len(nodes))
	sem := make(chan struct{}, nodePluginsConcurrency)
	var wg sync.WaitGroup
	for _, n := range nodes {
		known[n.Id] = true
		if !n.Enable || n.Status != "online" || !supportsPlugins(n) {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		common.GoRecover("node-plugins:"+n.Name, func() {
			defer wg.Done()
			defer func() { <-sem }()
			s.refresh(ctx, n)
		})
	}
	wg.Wait()
	s.cache.retain(known)
}

func (s *NodePluginsService) refresh(ctx context.Context, n *model.Node) {
	st := NodePluginStatus{NodeID: n.Id, NodeName: n.Name, State: NodePluginsOK, FetchedAt: time.Now().Unix()}
	client, err := s.Client(n)
	if err == nil {
		rctx, cancel := context.WithTimeout(ctx, nodePluginsTimeout)
		st.Plugins, err = client.PluginsList(rctx)
		cancel()
	}
	if err != nil {
		st.State, st.Error = NodePluginsError, err.Error()
		var refusal *runtime.RemotePluginError
		if errors.As(err, &refusal) && refusal.Status == http.StatusForbidden {
			st.State = NodePluginsLocked
		}
	}
	s.cache.put(st)
}

// target resolves a node that can take a command right now.
func (s *NodePluginsService) target(nodeID int) (*model.Node, NodePluginsClient, error) {
	n, err := (&NodeService{}).GetById(nodeID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, ErrNodeNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	switch {
	case !supportsPlugins(n):
		return nil, nil, ErrNodeUnsupported
	case !n.Enable || n.Status != "online":
		return nil, nil, ErrNodeUnavailable
	}
	client, err := s.Client(n)
	if err != nil {
		return nil, nil, err
	}
	return n, client, nil
}

// pinned builds the node request from this panel's own registry, never from the caller's input.
func (s *NodePluginsService) pinned(pluginID string, permissions []string) (runtime.RemotePluginInstall, error) {
	entries, err := s.Entries()
	if err != nil {
		return runtime.RemotePluginInstall{}, err
	}
	for _, e := range entries {
		if e.ID != pluginID {
			continue
		}
		switch {
		case e.Scope != plugins.ScopeNode && e.Scope != plugins.ScopeBoth:
			return runtime.RemotePluginInstall{}, fmt.Errorf("%w: scope is %q", plugins.ErrScopeNotAllowed, e.Scope)
		case e.Commit == "" || e.ManifestSHA256 == "":
			return runtime.RemotePluginInstall{}, ErrPluginNotPinnable
		case !plugins.SamePermissionSet(e.Permissions, permissions):
			return runtime.RemotePluginInstall{}, ErrPermissionsDiffer
		}
		src := e.Source
		src.Ref = ""
		return runtime.RemotePluginInstall{
			URL: src.String(), Commit: e.Commit, ManifestSHA256: e.ManifestSHA256, Permissions: e.Permissions,
		}, nil
	}
	return runtime.RemotePluginInstall{}, plugins.ErrNotFound
}

// command runs one node call and re-reads the node, so the UI sees the outcome at once.
func (s *NodePluginsService) command(ctx context.Context, nodeID int, run func(context.Context, NodePluginsClient) error) error {
	n, client, err := s.target(nodeID)
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
func (s *NodePluginsService) finish(ctx context.Context, n *model.Node) {
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
	n, client, err := s.target(nodeID)
	if err != nil {
		return err
	}
	step := func(run func(context.Context) error) error {
		sctx, cancel := context.WithTimeout(ctx, nodePluginsTimeout)
		defer cancel()
		return slowOr(run(sctx))
	}
	var current *runtime.RemotePlugin
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

// Refresh re-reads one node at once instead of waiting for the next poll.
func (s *NodePluginsService) Refresh(ctx context.Context, nodeID int) error {
	n, _, err := s.target(nodeID)
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
	_, client, err := s.target(nodeID)
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
