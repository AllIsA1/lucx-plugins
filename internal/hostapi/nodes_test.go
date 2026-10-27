// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AllIsA1/lucx-plugins/internal/plugins"
)

const (
	nodePluginCommit   = "0123456789abcdef0123456789abcdef01234567"
	nodePluginManifest = "feedfacefeedfacefeedfacefeedfacefeedfacefeedfacefeedfacefeedface"
)

type fakeNodeClient struct {
	mu         sync.Mutex
	state      NodePluginsState
	listErr    error
	commandErr error
	calls      []string
	installed  NodePluginInstall
	listAfter  [][]NodePlugin
	// updateMissing plays a node whose plugin system has no in-place update route yet; updateErr fails the update.
	updateMissing bool
	updateErr     error
	system        json.RawMessage
	systemErr     error
	systemPart    string
}

func (f *fakeNodeClient) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeNodeClient) callList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *fakeNodeClient) PluginsList(context.Context) (*NodePluginsState, error) {
	f.record("list")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	if len(f.listAfter) > 0 {
		f.state.Plugins, f.listAfter = f.listAfter[0], f.listAfter[1:]
	}
	st := f.state
	return &st, nil
}

func (f *fakeNodeClient) PluginsInstall(_ context.Context, req NodePluginInstall) error {
	f.record("install")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.installed = req
	if f.commandErr == nil {
		f.state.Plugins = append(f.state.Plugins, NodePlugin{ID: "blockads", Commit: req.Commit, ManagedByMaster: true})
	}
	return f.commandErr
}

func (f *fakeNodeClient) PluginsUpdate(_ context.Context, req NodePluginInstall) error {
	f.record("update")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.updateMissing {
		return &NodeRefusal{Status: http.StatusNotFound, Msg: "HTTP 404"}
	}
	if f.updateErr != nil {
		return f.updateErr
	}
	f.installed = req
	for i := range f.state.Plugins {
		if f.state.Plugins[i].ID == "blockads" {
			f.state.Plugins[i].Commit = req.Commit
		}
	}
	return nil
}

func (f *fakeNodeClient) SystemState(context.Context) (json.RawMessage, error) {
	f.record("system")
	return f.system, f.systemErr
}

func (f *fakeNodeClient) SystemCheck(context.Context) error {
	f.record("system check")
	return f.systemErr
}

func (f *fakeNodeClient) SystemUpdate(_ context.Context, part string) error {
	f.record("system update " + part)
	f.mu.Lock()
	f.systemPart = part
	f.mu.Unlock()
	return f.systemErr
}

func (f *fakeNodeClient) PluginsSetEnabled(_ context.Context, id string, enabled bool) error {
	if enabled {
		f.record("enable " + id)
	} else {
		f.record("disable " + id)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.commandErr == nil {
		for i := range f.state.Plugins {
			if f.state.Plugins[i].ID == id {
				f.state.Plugins[i].Enabled = enabled
			}
		}
	}
	return f.commandErr
}

func (f *fakeNodeClient) PluginsUninstall(_ context.Context, id string, removeData bool) error {
	if removeData {
		f.record("uninstall+data " + id)
	} else {
		f.record("uninstall " + id)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.commandErr == nil {
		f.state.Plugins = slices.DeleteFunc(f.state.Plugins, func(p NodePlugin) bool { return p.ID == id })
	}
	return f.commandErr
}

func (f *fakeNodeClient) PluginsRestart(_ context.Context, id string) error {
	f.record("restart " + id)
	return f.commandErr
}

func (f *fakeNodeClient) PluginsLogs(_ context.Context, id string, lines int) ([]string, error) {
	f.record("logs " + id)
	return []string{"line from " + id}, f.commandErr
}

func masterEntries(entries ...plugins.Entry) func() ([]plugins.Entry, error) {
	return func() ([]plugins.Entry, error) { return entries, nil }
}

func blockAdsEntry() plugins.Entry {
	return plugins.Entry{
		ID: "blockads", Name: "Block ads", Version: "1.2.0", Type: plugins.TypeProcess, Scope: plugins.ScopeBoth,
		Source: plugins.Source{Owner: "acme", Repo: "blockads", Ref: "main"}, Commit: nodePluginCommit,
		ManifestSHA256: nodePluginManifest, Permissions: []string{"network", "pipeline:xray.config"},
	}
}

// nodeBook is the master panel's node list as the tests see it.
type nodeBook struct {
	mu    sync.Mutex
	nodes []NodeInfo
	next  int
}

func (b *nodeBook) list(context.Context) ([]NodeInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.nodes), nil
}

func (b *nodeBook) add(name, status string, withFeature bool) NodeInfo {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.next++
	n := NodeInfo{ID: b.next, Name: name, Enable: true, Status: status}
	if withFeature {
		n.Features = []string{featurePlugins}
	}
	b.nodes = append(b.nodes, n)
	return n
}

func (b *nodeBook) remove(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nodes = slices.DeleteFunc(b.nodes, func(n NodeInfo) bool { return n.ID == id })
}

// fixtureBook is the book of the service most recently made by newNodePluginsFixture.
var fixtureBook *nodeBook

func pluginNode(t *testing.T, name, status string, withFeature bool) NodeInfo {
	t.Helper()
	return fixtureBook.add(name, status, withFeature)
}

func newNodePluginsFixture(t *testing.T, entries ...plugins.Entry) (*NodePluginsService, *fakeNodeClient) {
	t.Helper()
	fake := &fakeNodeClient{state: NodePluginsState{Available: true, AllowMasterManagement: true}}
	fixtureBook = &nodeBook{}
	svc := NewNodePluginsServiceFor(masterEntries(entries...), fixtureBook.list, func(NodeInfo) NodePluginsClient { return fake })
	return svc, fake
}

func statusOf(t *testing.T, svc *NodePluginsService, nodeID int) NodePluginStatus {
	t.Helper()
	all, err := svc.Statuses(context.Background())
	if err != nil {
		t.Fatalf("Statuses: %v", err)
	}
	for _, s := range all {
		if s.NodeID == nodeID {
			return s
		}
	}
	t.Fatalf("node %d missing from statuses %+v", nodeID, all)
	return NodePluginStatus{}
}

func TestNodePluginStatusesExplainWhyANodeCannotBeManaged(t *testing.T) {
	svc, fake := newNodePluginsFixture(t)
	old := pluginNode(t, "old", "online", false)
	down := pluginNode(t, "down", "offline", true)
	ready := pluginNode(t, "ready", "online", true)

	if got := statusOf(t, svc, old.ID).State; got != NodePluginsUnsupported {
		t.Errorf("a node that did not announce plugins is %q, want unsupported", got)
	}
	if got := statusOf(t, svc, down.ID).State; got != NodePluginsOffline {
		t.Errorf("an offline node is %q, want offline", got)
	}
	if got := statusOf(t, svc, ready.ID).State; got != NodePluginsPending {
		t.Errorf("a node nobody polled yet is %q, want pending", got)
	}

	svc.Poll(context.Background())
	if calls := fake.callList(); len(calls) != 1 {
		t.Errorf("polling asked %v, want exactly one list for the one reachable node", calls)
	}
	if got := statusOf(t, svc, ready.ID); got.State != NodePluginsOK || got.Plugins == nil || !got.Plugins.AllowMasterManagement || got.FetchedAt == 0 {
		t.Errorf("polled node = %+v, want ok with the node's state and a timestamp", got)
	}
}

func TestNodePluginPollKeepsTheFailureAndRecovers(t *testing.T) {
	svc, fake := newNodePluginsFixture(t)
	n := pluginNode(t, "flaky", "online", true)

	fake.listErr = errors.New("connection refused")
	svc.Poll(context.Background())
	if got := statusOf(t, svc, n.ID); got.State != NodePluginsError || got.Error != "connection refused" {
		t.Errorf("failed poll = %+v, want error with the cause", got)
	}

	fake.listErr = nil
	svc.Poll(context.Background())
	if got := statusOf(t, svc, n.ID); got.State != NodePluginsOK || got.Error != "" {
		t.Errorf("recovered poll = %+v, want ok and no stale error", got)
	}
}

func TestNodePluginPollForgetsDeletedNodes(t *testing.T) {
	svc, _ := newNodePluginsFixture(t)
	n := pluginNode(t, "gone", "online", true)
	svc.Poll(context.Background())
	fixtureBook.remove(n.ID)
	svc.Poll(context.Background())
	if svc.cache.size() != 0 {
		t.Errorf("cache still holds %d entries for a deleted node", svc.cache.size())
	}
}

func TestNodePluginInstallPinsWhatTheMasterHas(t *testing.T) {
	svc, fake := newNodePluginsFixture(t, blockAdsEntry())
	n := pluginNode(t, "edge", "online", true)

	// The permissions come from the UI in a different order; the set is what counts.
	err := svc.Install(context.Background(), n.ID, "blockads", []string{"pipeline:xray.config", "network"})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	want := NodePluginInstall{
		URL: "https://github.com/acme/blockads", Commit: nodePluginCommit, ManifestSHA256: nodePluginManifest,
		Permissions: []string{"network", "pipeline:xray.config"},
	}
	got := fake.installed
	slices.Sort(got.Permissions)
	if got.URL != want.URL || got.Commit != want.Commit || got.ManifestSHA256 != want.ManifestSHA256 || !slices.Equal(got.Permissions, want.Permissions) {
		t.Errorf("node was told %+v, want %+v (a branch ref must never travel)", fake.installed, want)
	}
	if st := statusOf(t, svc, n.ID); st.State != NodePluginsOK || st.Plugins == nil || len(st.Plugins.Plugins) != 1 {
		t.Errorf("status after install = %+v, want the node re-read with the new plugin", st)
	}
}

func TestNodePluginInstallRefusals(t *testing.T) {
	masterOnly := blockAdsEntry()
	masterOnly.ID, masterOnly.Scope = "masteronly", plugins.ScopeMaster
	unhashed := blockAdsEntry()
	unhashed.ID, unhashed.ManifestSHA256 = "unhashed", ""

	tests := []struct {
		name   string
		plugin string
		perms  []string
		node   func(*testing.T) NodeInfo
		want   error
	}{
		{"unknown plugin", "nope", []string{"network", "pipeline:xray.config"}, readyNode, plugins.ErrNotFound},
		{"a master-only plugin", "masteronly", []string{"network", "pipeline:xray.config"}, readyNode, plugins.ErrScopeNotAllowed},
		{"missing manifest hash", "unhashed", []string{"network", "pipeline:xray.config"}, readyNode, ErrPluginNotPinnable},
		{"fewer permissions than granted", "blockads", []string{"network"}, readyNode, ErrPermissionsDiffer},
		{"more permissions than granted", "blockads", []string{"network", "pipeline:xray.config", "api:nodes:write"}, readyNode, ErrPermissionsDiffer},
		{"node without the feature", "blockads", []string{"network", "pipeline:xray.config"}, func(t *testing.T) NodeInfo { return pluginNode(t, "old", "online", false) }, ErrNodeUnsupported},
		{"offline node", "blockads", []string{"network", "pipeline:xray.config"}, func(t *testing.T) NodeInfo { return pluginNode(t, "down", "offline", true) }, ErrNodeUnavailable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, fake := newNodePluginsFixture(t, blockAdsEntry(), masterOnly, unhashed)
			n := tc.node(t)
			err := svc.Install(context.Background(), n.ID, tc.plugin, tc.perms)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if calls := fake.callList(); len(calls) != 0 {
				t.Errorf("the node was contacted (%v) although the request was refused", calls)
			}
		})
	}

	t.Run("unknown node", func(t *testing.T) {
		svc, _ := newNodePluginsFixture(t, blockAdsEntry())
		if err := svc.Install(context.Background(), 4242, "blockads", []string{"network", "pipeline:xray.config"}); !errors.Is(err, ErrNodeNotFound) {
			t.Fatalf("err = %v, want ErrNodeNotFound", err)
		}
	})
}

func readyNode(t *testing.T) NodeInfo { return pluginNode(t, "ready", "online", true) }

func TestNodePluginInstallSurfacesTheNodesRefusal(t *testing.T) {
	svc, fake := newNodePluginsFixture(t, blockAdsEntry())
	n := readyNode(t)
	fake.commandErr = errors.New("plugin management by the master is disabled on this node")

	err := svc.Install(context.Background(), n.ID, "blockads", []string{"network", "pipeline:xray.config"})
	if err == nil || err.Error() != "plugin management by the master is disabled on this node" {
		t.Fatalf("err = %v, want the node's own reason", err)
	}
}

func TestNodePluginCommandsReachTheNodeAndRefreshIt(t *testing.T) {
	svc, fake := newNodePluginsFixture(t)
	n := readyNode(t)
	ctx := context.Background()

	for _, step := range []struct {
		name string
		run  func() error
		want string
	}{
		{"enable", func() error { return svc.SetEnabled(ctx, n.ID, "blockads", true) }, "enable blockads"},
		{"disable", func() error { return svc.SetEnabled(ctx, n.ID, "blockads", false) }, "disable blockads"},
		{"restart", func() error { return svc.Restart(ctx, n.ID, "blockads") }, "restart blockads"},
		{"uninstall", func() error { return svc.Uninstall(ctx, n.ID, "blockads", false) }, "uninstall blockads"},
		{"uninstall with data", func() error { return svc.Uninstall(ctx, n.ID, "blockads", true) }, "uninstall+data blockads"},
	} {
		before := len(fake.callList())
		if err := step.run(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		calls := fake.callList()[before:]
		if !slices.Equal(calls, []string{step.want, "list"}) {
			t.Errorf("%s sent %v, want the command and then a re-read", step.name, calls)
		}
	}

	lines, err := svc.Logs(ctx, n.ID, "blockads", 50)
	if err != nil || !slices.Equal(lines, []string{"line from blockads"}) {
		t.Errorf("Logs = %v, %v", lines, err)
	}
}

func TestNodePluginCommandsNeedAReachableSupportingNode(t *testing.T) {
	svc, fake := newNodePluginsFixture(t)
	old := pluginNode(t, "old", "online", false)
	down := pluginNode(t, "down", "offline", true)
	ctx := context.Background()

	if err := svc.Restart(ctx, old.ID, "blockads"); !errors.Is(err, ErrNodeUnsupported) {
		t.Errorf("restart on an old node = %v, want ErrNodeUnsupported", err)
	}
	if err := svc.SetEnabled(ctx, down.ID, "blockads", true); !errors.Is(err, ErrNodeUnavailable) {
		t.Errorf("enable on an offline node = %v, want ErrNodeUnavailable", err)
	}
	if _, err := svc.Logs(ctx, down.ID, "blockads", 10); !errors.Is(err, ErrNodeUnavailable) {
		t.Errorf("logs on an offline node = %v, want ErrNodeUnavailable", err)
	}
	if calls := fake.callList(); len(calls) != 0 {
		t.Errorf("unreachable nodes were contacted: %v", calls)
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestNodePluginInstallTimeoutMeansItMayStillBeRunning(t *testing.T) {
	for _, cause := range []error{context.DeadlineExceeded, timeoutErr{}} {
		svc, fake := newNodePluginsFixture(t, blockAdsEntry())
		n := readyNode(t)
		fake.commandErr = cause

		err := svc.Install(context.Background(), n.ID, "blockads", []string{"network", "pipeline:xray.config"})
		if !errors.Is(err, ErrNodeSlow) {
			t.Errorf("%T: err = %v, want ErrNodeSlow", cause, err)
		}
	}
}

func TestNodePluginPollSeparatesALockedNodeFromABrokenOne(t *testing.T) {
	svc, fake := newNodePluginsFixture(t)
	n := pluginNode(t, "locked", "online", true)

	fake.listErr = &NodeRefusal{Status: http.StatusForbidden, Msg: "plugin management by the master is disabled on this node"}
	svc.Poll(context.Background())
	if got := statusOf(t, svc, n.ID); got.State != NodePluginsLocked || got.Error != "plugin management by the master is disabled on this node" {
		t.Errorf("a node that refuses the master = %+v, want locked with its own words", got)
	}

	fake.listErr = &NodeRefusal{Status: http.StatusInternalServerError, Msg: "boom"}
	svc.Poll(context.Background())
	if got := statusOf(t, svc, n.ID); got.State != NodePluginsError {
		t.Errorf("a failing node = %+v, want error", got)
	}
}

func TestNodePluginCommandRereadsTheNodeOnceItSettled(t *testing.T) {
	svc, fake := newNodePluginsFixture(t)
	svc.SettleDelay = 10 * time.Millisecond
	n := readyNode(t)
	running := NodePlugin{ID: "blockads", Enabled: false, Runtime: &NodePluginRuntime{State: "running"}}
	stopped := NodePlugin{ID: "blockads", Enabled: false, Runtime: &NodePluginRuntime{State: "stopped"}}
	fake.listAfter = [][]NodePlugin{{running}, {stopped}}

	if err := svc.SetEnabled(context.Background(), n.ID, "blockads", false); err != nil {
		t.Fatal(err)
	}
	state := func() string {
		st := statusOf(t, svc, n.ID)
		if st.Plugins == nil || len(st.Plugins.Plugins) != 1 {
			return ""
		}
		return st.Plugins.Plugins[0].Runtime.State
	}
	if got := state(); got != "running" {
		t.Fatalf("right after the command the node still reports %q, want its process not yet stopped", got)
	}
	deadline := time.Now().Add(5 * time.Second)
	for state() != "stopped" {
		if time.Now().After(deadline) {
			t.Fatal("the node was not read again after it settled")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

const oldNodeCommit = "ffffffffffffffffffffffffffffffffffffffff"

// onTheNode puts blockads on the fake node at an old commit.
func onTheNode(fake *fakeNodeClient, mutate func(p *NodePlugin)) {
	p := NodePlugin{ID: "blockads", Commit: oldNodeCommit, ManagedByMaster: true, Enabled: true, Permissions: []string{"network"}}
	if mutate != nil {
		mutate(&p)
	}
	fake.state.Plugins = []NodePlugin{p}
}

func commandsOf(fake *fakeNodeClient) []string {
	var out []string
	for _, c := range fake.callList() {
		if c != "list" {
			out = append(out, c)
		}
	}
	return out
}

func TestNodePluginUpdateMovesThePluginInPlaceWithoutRemovingIt(t *testing.T) {
	svc, fake := newNodePluginsFixture(t, blockAdsEntry())
	n := readyNode(t)
	onTheNode(fake, nil)

	if err := svc.Update(context.Background(), n.ID, "blockads", blockAdsEntry().Permissions); err != nil {
		t.Fatal(err)
	}
	if got := commandsOf(fake); !slices.Equal(got, []string{"update"}) {
		t.Errorf("node commands = %v, want one in-place update: removing the plugin would fail while others require it", got)
	}
	if fake.installed.Commit != nodePluginCommit || fake.installed.ManifestSHA256 != nodePluginManifest {
		t.Errorf("updated to %+v, want the master's commit and manifest", fake.installed)
	}
	if st := statusOf(t, svc, n.ID); st.Plugins == nil || st.Plugins.Plugins[0].Commit != nodePluginCommit || !st.Plugins.Plugins[0].Enabled {
		t.Errorf("status after the update = %+v", st.Plugins)
	}
}

func TestNodePluginUpdateReportsAnInPlaceFailureWithoutFallingBack(t *testing.T) {
	svc, fake := newNodePluginsFixture(t, blockAdsEntry())
	n := readyNode(t)
	onTheNode(fake, nil)
	fake.updateErr = &NodeRefusal{Status: http.StatusOK, Msg: "plugins: update failed: the new process did not become healthy"}
	err := svc.Update(context.Background(), n.ID, "blockads", blockAdsEntry().Permissions)
	if err == nil || !strings.Contains(err.Error(), "did not become healthy") {
		t.Fatalf("err = %v, want the node's own words", err)
	}
	if got := commandsOf(fake); !slices.Equal(got, []string{"update"}) {
		t.Errorf("node commands = %v, want no removal after a refused update", got)
	}
}

func TestNodePluginUpdateReinstallsTheMastersCommitAndKeepsItRunning(t *testing.T) {
	svc, fake := newNodePluginsFixture(t, blockAdsEntry())
	n := readyNode(t)
	onTheNode(fake, nil)
	fake.updateMissing = true // a node that has not got the in-place route yet

	if err := svc.Update(context.Background(), n.ID, "blockads", blockAdsEntry().Permissions); err != nil {
		t.Fatal(err)
	}
	if got := commandsOf(fake); !slices.Equal(got, []string{"update", "uninstall blockads", "install", "enable blockads"}) {
		t.Errorf("node commands = %v, want the in-place try, then uninstall (data kept), install, enable again", got)
	}
	if fake.installed.Commit != nodePluginCommit || fake.installed.ManifestSHA256 != nodePluginManifest {
		t.Errorf("installed %+v, want the master's commit and manifest", fake.installed)
	}
	st := statusOf(t, svc, n.ID)
	if st.Plugins == nil || len(st.Plugins.Plugins) != 1 || st.Plugins.Plugins[0].Commit != nodePluginCommit || !st.Plugins.Plugins[0].Enabled {
		t.Errorf("status after the update = %+v", st.Plugins)
	}
}

func TestNodePluginUpdateKeepsADisabledPluginDisabled(t *testing.T) {
	svc, fake := newNodePluginsFixture(t, blockAdsEntry())
	n := readyNode(t)
	onTheNode(fake, func(p *NodePlugin) { p.Enabled = false })
	fake.updateMissing = true
	if err := svc.Update(context.Background(), n.ID, "blockads", blockAdsEntry().Permissions); err != nil {
		t.Fatal(err)
	}
	if got := commandsOf(fake); !slices.Equal(got, []string{"update", "uninstall blockads", "install"}) {
		t.Errorf("node commands = %v, want no enable for a plugin that was off", got)
	}
}

func TestNodePluginUpdateRefusesBeforeTouchingTheNode(t *testing.T) {
	masterOnly := blockAdsEntry()
	masterOnly.Scope = plugins.ScopeMaster
	tests := []struct {
		name   string
		entry  plugins.Entry
		perms  []string
		onNode func(p *NodePlugin)
		absent bool
		want   error
	}{
		{"already on the master's commit", blockAdsEntry(), blockAdsEntry().Permissions, func(p *NodePlugin) { p.Commit = nodePluginCommit }, false, ErrNodeUpToDate},
		{"not installed on the node", blockAdsEntry(), blockAdsEntry().Permissions, nil, true, ErrNotOnNode},
		{"installed by hand on the node", blockAdsEntry(), blockAdsEntry().Permissions, func(p *NodePlugin) { p.ManagedByMaster = false }, false, ErrNotManagedByMaster},
		{"permissions other than the master's", blockAdsEntry(), []string{"network"}, nil, false, ErrPermissionsDiffer},
		{"a plugin that cannot run on a node", masterOnly, masterOnly.Permissions, nil, false, plugins.ErrScopeNotAllowed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, fake := newNodePluginsFixture(t, tc.entry)
			n := readyNode(t)
			if !tc.absent {
				onTheNode(fake, tc.onNode)
			}
			if err := svc.Update(context.Background(), n.ID, "blockads", tc.perms); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if got := commandsOf(fake); len(got) != 0 {
				t.Errorf("the node was changed (%v) although the update was refused", got)
			}
		})
	}
}

func TestNodePluginUpdateNeedsAReachableSupportingNode(t *testing.T) {
	svc, fake := newNodePluginsFixture(t, blockAdsEntry())
	old := pluginNode(t, "old", "online", false)
	down := pluginNode(t, "down", "offline", true)
	if err := svc.Update(context.Background(), old.ID, "blockads", blockAdsEntry().Permissions); !errors.Is(err, ErrNodeUnsupported) {
		t.Errorf("old node: %v", err)
	}
	if err := svc.Update(context.Background(), down.ID, "blockads", blockAdsEntry().Permissions); !errors.Is(err, ErrNodeUnavailable) {
		t.Errorf("offline node: %v", err)
	}
	if got := fake.callList(); len(got) != 0 {
		t.Errorf("unreachable nodes were contacted: %v", got)
	}
}

type failOn struct {
	*fakeNodeClient
	failCall string
	err      error
}

func (f *failOn) PluginsUninstall(ctx context.Context, id string, removeData bool) error {
	if f.failCall == "uninstall" {
		f.record("uninstall " + id)
		return f.err
	}
	return f.fakeNodeClient.PluginsUninstall(ctx, id, removeData)
}

func (f *failOn) PluginsInstall(ctx context.Context, req NodePluginInstall) error {
	if f.failCall == "install" {
		f.record("install")
		return f.err
	}
	return f.fakeNodeClient.PluginsInstall(ctx, req)
}

func (f *failOn) PluginsSetEnabled(ctx context.Context, id string, enabled bool) error {
	if f.failCall == "enable" {
		f.record("enable " + id)
		return f.err
	}
	return f.fakeNodeClient.PluginsSetEnabled(ctx, id, enabled)
}

func newFailingUpdate(t *testing.T, failCall string) (*NodePluginsService, *failOn, int) {
	t.Helper()
	fixtureBook = &nodeBook{}
	fake := &failOn{fakeNodeClient: &fakeNodeClient{state: NodePluginsState{Available: true, AllowMasterManagement: true}}, failCall: failCall, err: errors.New("download failed")}
	onTheNode(fake.fakeNodeClient, nil)
	fake.updateMissing = true // these failures are those of the remove-and-install way, for nodes without the in-place route
	svc := NewNodePluginsServiceFor(masterEntries(blockAdsEntry()), fixtureBook.list, func(NodeInfo) NodePluginsClient { return fake })
	return svc, fake, readyNode(t).ID
}

func TestNodePluginUpdateStopsAtTheFirstFailureAndSaysWhatState(t *testing.T) {
	ctx := context.Background()
	perms := blockAdsEntry().Permissions

	svc, fake, id := newFailingUpdate(t, "uninstall")
	if err := svc.Update(ctx, id, "blockads", perms); err == nil || errors.Is(err, ErrNodeUpdateIncomplete) {
		t.Errorf("a refused uninstall = %v, want a plain failure: the old version is still there", err)
	}
	if got := commandsOf(fake.fakeNodeClient); !slices.Equal(got, []string{"update", "uninstall blockads"}) {
		t.Errorf("commands = %v, want no install after a failed uninstall", got)
	}

	svc, fake, id = newFailingUpdate(t, "install")
	err := svc.Update(ctx, id, "blockads", perms)
	if !errors.Is(err, ErrNodeUpdateIncomplete) || !strings.Contains(err.Error(), "download failed") {
		t.Errorf("a failed install = %v, want ErrNodeUpdateIncomplete with the cause", err)
	}
	if got := commandsOf(fake.fakeNodeClient); !slices.Equal(got, []string{"update", "uninstall blockads", "install"}) {
		t.Errorf("commands = %v", got)
	}
	if st := statusOf(t, svc, id); st.Plugins == nil || len(st.Plugins.Plugins) != 0 {
		t.Errorf("status = %+v, want the plugin shown as gone from the node", st.Plugins)
	}

	svc, _, id = newFailingUpdate(t, "enable")
	if err := svc.Update(ctx, id, "blockads", perms); !errors.Is(err, ErrNodeUpdateIncomplete) {
		t.Errorf("a failed re-enable = %v, want ErrNodeUpdateIncomplete", err)
	}
}

func TestNodeSystemIsReadCheckedAndUpdatedThroughTheNode(t *testing.T) {
	svc, fake := newNodePluginsFixture(t)
	n := readyNode(t)
	fake.system = json.RawMessage(`{"managed":true,"version":"host-6","available":"host-7"}`)
	ctx := context.Background()

	obj, err := svc.System(ctx, n.ID)
	if err != nil || string(obj) != string(fake.system) {
		t.Fatalf("System = %s, %v", obj, err)
	}
	if err := svc.SystemCheck(ctx, n.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.SystemUpdate(ctx, n.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.SystemUpdate(ctx, n.ID, "panel"); err != nil {
		t.Fatal(err)
	}
	if got := fake.callList(); !slices.Equal(got, []string{"system", "system check", "system update ", "system update panel"}) {
		t.Errorf("node calls = %v", got)
	}
	if err := svc.SystemUpdate(ctx, n.ID, "everything"); err == nil {
		t.Error("an unknown part was accepted")
	}
}

func TestNodeSystemUpdateNeedsAReachableSupportingNode(t *testing.T) {
	svc, fake := newNodePluginsFixture(t)
	old := pluginNode(t, "old", "online", false)
	down := pluginNode(t, "down", "offline", true)
	ctx := context.Background()
	if err := svc.SystemUpdate(ctx, old.ID, ""); !errors.Is(err, ErrNodeUnsupported) {
		t.Errorf("old node: %v", err)
	}
	if err := svc.SystemUpdate(ctx, down.ID, ""); !errors.Is(err, ErrNodeUnavailable) {
		t.Errorf("offline node: %v", err)
	}
	if _, err := svc.System(ctx, down.ID); !errors.Is(err, ErrNodeUnavailable) {
		t.Errorf("System of an offline node: %v", err)
	}
	if got := fake.callList(); len(got) != 0 {
		t.Errorf("unreachable nodes were contacted: %v", got)
	}
}

func TestNodeSystemUpdateKeepsTheNodesRefusalAndForgivesAPanelThatRestarts(t *testing.T) {
	svc, fake := newNodePluginsFixture(t)
	n := readyNode(t)
	ctx := context.Background()

	fake.systemErr = &NodeRefusal{Status: http.StatusForbidden, Msg: "plugin management by the master is disabled on this node"}
	if err := svc.SystemUpdate(ctx, n.ID, ""); err == nil || !strings.Contains(err.Error(), "disabled on this node") {
		t.Errorf("a refusal = %v, want the node's words", err)
	}
	if err := svc.SystemUpdate(ctx, n.ID, "panel"); err == nil {
		t.Error("a refusal of the panel update was swallowed")
	}

	fake.systemErr = errors.New("the node did not answer: EOF") // the node's panel restarting under the call
	if err := svc.SystemUpdate(ctx, n.ID, "panel"); err != nil {
		t.Errorf("a panel that restarts under the call is the expected outcome, got %v", err)
	}
	if err := svc.SystemUpdate(ctx, n.ID, ""); err == nil {
		t.Error("a failure to reach the node for a plugin system update was hidden")
	}
}

func TestRouteMissingTellsAnOldNodeFromARefusal(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{&NodeRefusal{Status: http.StatusNotFound, Msg: "HTTP 404"}, true},
		{&NodeRefusal{Status: http.StatusForbidden, Msg: "this plugin route is not available to a master panel"}, true},
		{&NodeRefusal{Status: http.StatusForbidden, Msg: "plugin management by the master is disabled on this node"}, false},
		{&NodeRefusal{Status: http.StatusBadRequest, Msg: "plugins: update failed"}, false},
		{errors.New("timeout"), false},
		{nil, false},
	} {
		if got := routeMissing(tc.err); got != tc.want {
			t.Errorf("routeMissing(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}
