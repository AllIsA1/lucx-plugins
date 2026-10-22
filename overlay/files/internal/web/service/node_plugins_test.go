// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package service

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/lucx/nodetype"
	"github.com/mhsanaei/3x-ui/v3/internal/lucx/plugins"
	"github.com/mhsanaei/3x-ui/v3/internal/web/runtime"
)

const (
	nodePluginCommit   = "0123456789abcdef0123456789abcdef01234567"
	nodePluginManifest = "feedfacefeedfacefeedfacefeedfacefeedfacefeedfacefeedfacefeedface"
)

type fakeNodePlugins struct {
	mu         sync.Mutex
	state      runtime.RemotePluginsState
	listErr    error
	commandErr error
	calls      []string
	installed  runtime.RemotePluginInstall
	listAfter  [][]runtime.RemotePlugin
}

func (f *fakeNodePlugins) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeNodePlugins) callList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *fakeNodePlugins) PluginsList(context.Context) (*runtime.RemotePluginsState, error) {
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

func (f *fakeNodePlugins) PluginsInstall(_ context.Context, req runtime.RemotePluginInstall) error {
	f.record("install")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.installed = req
	if f.commandErr == nil {
		f.state.Plugins = append(f.state.Plugins, runtime.RemotePlugin{ID: "blockads", Commit: req.Commit, ManagedByMaster: true})
	}
	return f.commandErr
}

func (f *fakeNodePlugins) PluginsSetEnabled(_ context.Context, id string, enabled bool) error {
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

func (f *fakeNodePlugins) PluginsUninstall(_ context.Context, id string, removeData bool) error {
	if removeData {
		f.record("uninstall+data " + id)
	} else {
		f.record("uninstall " + id)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.commandErr == nil {
		f.state.Plugins = slices.DeleteFunc(f.state.Plugins, func(p runtime.RemotePlugin) bool { return p.ID == id })
	}
	return f.commandErr
}

func (f *fakeNodePlugins) PluginsRestart(_ context.Context, id string) error {
	f.record("restart " + id)
	return f.commandErr
}

func (f *fakeNodePlugins) PluginsLogs(_ context.Context, id string, lines int) ([]string, error) {
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

func pluginNode(t *testing.T, name, status string, withFeature bool) *model.Node {
	t.Helper()
	info := &nodetype.NodeInfo{NodeType: nodetype.TypeLucX}
	if withFeature {
		info.Features = []string{nodetype.FeaturePlugins}
	}
	n := &model.Node{
		Name: name, Scheme: "https", Address: name + ".example.com", Port: 2053, Enable: true,
		Status: status, Features: info.ToJSON(),
	}
	seedNodeRow(t, database.GetDB(), n)
	return n
}

func newNodePluginsFixture(t *testing.T, entries ...plugins.Entry) (*NodePluginsService, *fakeNodePlugins) {
	t.Helper()
	setupBulkDB(t)
	fake := &fakeNodePlugins{state: runtime.RemotePluginsState{Available: true, AllowMasterManagement: true}}
	svc := NewNodePluginsServiceFor(masterEntries(entries...), func(*model.Node) (NodePluginsClient, error) { return fake, nil })
	return svc, fake
}

func statusOf(t *testing.T, svc *NodePluginsService, nodeID int) NodePluginStatus {
	t.Helper()
	all, err := svc.Statuses()
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

	if got := statusOf(t, svc, old.Id).State; got != NodePluginsUnsupported {
		t.Errorf("a node that did not announce plugins is %q, want unsupported", got)
	}
	if got := statusOf(t, svc, down.Id).State; got != NodePluginsOffline {
		t.Errorf("an offline node is %q, want offline", got)
	}
	if got := statusOf(t, svc, ready.Id).State; got != NodePluginsPending {
		t.Errorf("a node nobody polled yet is %q, want pending", got)
	}

	svc.Poll(context.Background())
	if calls := fake.callList(); len(calls) != 1 {
		t.Errorf("polling asked %v, want exactly one list for the one reachable node", calls)
	}
	if got := statusOf(t, svc, ready.Id); got.State != NodePluginsOK || got.Plugins == nil || !got.Plugins.AllowMasterManagement || got.FetchedAt == 0 {
		t.Errorf("polled node = %+v, want ok with the node's state and a timestamp", got)
	}
}

func TestNodePluginPollKeepsTheFailureAndRecovers(t *testing.T) {
	svc, fake := newNodePluginsFixture(t)
	n := pluginNode(t, "flaky", "online", true)

	fake.listErr = errors.New("connection refused")
	svc.Poll(context.Background())
	if got := statusOf(t, svc, n.Id); got.State != NodePluginsError || got.Error != "connection refused" {
		t.Errorf("failed poll = %+v, want error with the cause", got)
	}

	fake.listErr = nil
	svc.Poll(context.Background())
	if got := statusOf(t, svc, n.Id); got.State != NodePluginsOK || got.Error != "" {
		t.Errorf("recovered poll = %+v, want ok and no stale error", got)
	}
}

func TestNodePluginPollForgetsDeletedNodes(t *testing.T) {
	svc, _ := newNodePluginsFixture(t)
	n := pluginNode(t, "gone", "online", true)
	svc.Poll(context.Background())
	if err := database.GetDB().Delete(&model.Node{}, n.Id).Error; err != nil {
		t.Fatal(err)
	}
	svc.Poll(context.Background())
	if svc.cache.size() != 0 {
		t.Errorf("cache still holds %d entries for a deleted node", svc.cache.size())
	}
}

func TestNodePluginInstallPinsWhatTheMasterHas(t *testing.T) {
	svc, fake := newNodePluginsFixture(t, blockAdsEntry())
	n := pluginNode(t, "edge", "online", true)

	// The permissions come from the UI in a different order; the set is what counts.
	err := svc.Install(context.Background(), n.Id, "blockads", []string{"pipeline:xray.config", "network"})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	want := runtime.RemotePluginInstall{
		URL: "https://github.com/acme/blockads", Commit: nodePluginCommit, ManifestSHA256: nodePluginManifest,
		Permissions: []string{"network", "pipeline:xray.config"},
	}
	got := fake.installed
	slices.Sort(got.Permissions)
	if got.URL != want.URL || got.Commit != want.Commit || got.ManifestSHA256 != want.ManifestSHA256 || !slices.Equal(got.Permissions, want.Permissions) {
		t.Errorf("node was told %+v, want %+v (a branch ref must never travel)", fake.installed, want)
	}
	if st := statusOf(t, svc, n.Id); st.State != NodePluginsOK || st.Plugins == nil || len(st.Plugins.Plugins) != 1 {
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
		node   func(*testing.T) *model.Node
		want   error
	}{
		{"unknown plugin", "nope", []string{"network", "pipeline:xray.config"}, readyNode, plugins.ErrNotFound},
		{"a master-only plugin", "masteronly", []string{"network", "pipeline:xray.config"}, readyNode, plugins.ErrScopeNotAllowed},
		{"missing manifest hash", "unhashed", []string{"network", "pipeline:xray.config"}, readyNode, ErrPluginNotPinnable},
		{"fewer permissions than granted", "blockads", []string{"network"}, readyNode, ErrPermissionsDiffer},
		{"more permissions than granted", "blockads", []string{"network", "pipeline:xray.config", "api:nodes:write"}, readyNode, ErrPermissionsDiffer},
		{"node without the feature", "blockads", []string{"network", "pipeline:xray.config"}, func(t *testing.T) *model.Node { return pluginNode(t, "old", "online", false) }, ErrNodeUnsupported},
		{"offline node", "blockads", []string{"network", "pipeline:xray.config"}, func(t *testing.T) *model.Node { return pluginNode(t, "down", "offline", true) }, ErrNodeUnavailable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, fake := newNodePluginsFixture(t, blockAdsEntry(), masterOnly, unhashed)
			n := tc.node(t)
			err := svc.Install(context.Background(), n.Id, tc.plugin, tc.perms)
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

func readyNode(t *testing.T) *model.Node { return pluginNode(t, "ready", "online", true) }

func TestNodePluginInstallSurfacesTheNodesRefusal(t *testing.T) {
	svc, fake := newNodePluginsFixture(t, blockAdsEntry())
	n := readyNode(t)
	fake.commandErr = errors.New("plugin management by the master is disabled on this node")

	err := svc.Install(context.Background(), n.Id, "blockads", []string{"network", "pipeline:xray.config"})
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
		{"enable", func() error { return svc.SetEnabled(ctx, n.Id, "blockads", true) }, "enable blockads"},
		{"disable", func() error { return svc.SetEnabled(ctx, n.Id, "blockads", false) }, "disable blockads"},
		{"restart", func() error { return svc.Restart(ctx, n.Id, "blockads") }, "restart blockads"},
		{"uninstall", func() error { return svc.Uninstall(ctx, n.Id, "blockads", false) }, "uninstall blockads"},
		{"uninstall with data", func() error { return svc.Uninstall(ctx, n.Id, "blockads", true) }, "uninstall+data blockads"},
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

	lines, err := svc.Logs(ctx, n.Id, "blockads", 50)
	if err != nil || !slices.Equal(lines, []string{"line from blockads"}) {
		t.Errorf("Logs = %v, %v", lines, err)
	}
}

func TestNodePluginCommandsNeedAReachableSupportingNode(t *testing.T) {
	svc, fake := newNodePluginsFixture(t)
	old := pluginNode(t, "old", "online", false)
	down := pluginNode(t, "down", "offline", true)
	ctx := context.Background()

	if err := svc.Restart(ctx, old.Id, "blockads"); !errors.Is(err, ErrNodeUnsupported) {
		t.Errorf("restart on an old node = %v, want ErrNodeUnsupported", err)
	}
	if err := svc.SetEnabled(ctx, down.Id, "blockads", true); !errors.Is(err, ErrNodeUnavailable) {
		t.Errorf("enable on an offline node = %v, want ErrNodeUnavailable", err)
	}
	if _, err := svc.Logs(ctx, down.Id, "blockads", 10); !errors.Is(err, ErrNodeUnavailable) {
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

		err := svc.Install(context.Background(), n.Id, "blockads", []string{"network", "pipeline:xray.config"})
		if !errors.Is(err, ErrNodeSlow) {
			t.Errorf("%T: err = %v, want ErrNodeSlow", cause, err)
		}
	}
}

func TestNodePluginPollSeparatesALockedNodeFromABrokenOne(t *testing.T) {
	svc, fake := newNodePluginsFixture(t)
	n := pluginNode(t, "locked", "online", true)

	fake.listErr = &runtime.RemotePluginError{Status: http.StatusForbidden, Msg: "plugin management by the master is disabled on this node"}
	svc.Poll(context.Background())
	if got := statusOf(t, svc, n.Id); got.State != NodePluginsLocked || got.Error != "plugin management by the master is disabled on this node" {
		t.Errorf("a node that refuses the master = %+v, want locked with its own words", got)
	}

	fake.listErr = &runtime.RemotePluginError{Status: http.StatusInternalServerError, Msg: "boom"}
	svc.Poll(context.Background())
	if got := statusOf(t, svc, n.Id); got.State != NodePluginsError {
		t.Errorf("a failing node = %+v, want error", got)
	}
}

func TestNodePluginCommandRereadsTheNodeOnceItSettled(t *testing.T) {
	svc, fake := newNodePluginsFixture(t)
	svc.SettleDelay = 10 * time.Millisecond
	n := readyNode(t)
	running := runtime.RemotePlugin{ID: "blockads", Enabled: false, Runtime: &runtime.RemotePluginRuntime{State: "running"}}
	stopped := runtime.RemotePlugin{ID: "blockads", Enabled: false, Runtime: &runtime.RemotePluginRuntime{State: "stopped"}}
	fake.listAfter = [][]runtime.RemotePlugin{{running}, {stopped}}

	if err := svc.SetEnabled(context.Background(), n.Id, "blockads", false); err != nil {
		t.Fatal(err)
	}
	state := func() string {
		st := statusOf(t, svc, n.Id)
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
func onTheNode(fake *fakeNodePlugins, mutate func(p *runtime.RemotePlugin)) {
	p := runtime.RemotePlugin{ID: "blockads", Commit: oldNodeCommit, ManagedByMaster: true, Enabled: true, Permissions: []string{"network"}}
	if mutate != nil {
		mutate(&p)
	}
	fake.state.Plugins = []runtime.RemotePlugin{p}
}

func commandsOf(fake *fakeNodePlugins) []string {
	var out []string
	for _, c := range fake.callList() {
		if c != "list" {
			out = append(out, c)
		}
	}
	return out
}

func TestNodePluginUpdateReinstallsTheMastersCommitAndKeepsItRunning(t *testing.T) {
	svc, fake := newNodePluginsFixture(t, blockAdsEntry())
	n := readyNode(t)
	onTheNode(fake, nil)

	if err := svc.Update(context.Background(), n.Id, "blockads", blockAdsEntry().Permissions); err != nil {
		t.Fatal(err)
	}
	if got := commandsOf(fake); !slices.Equal(got, []string{"uninstall blockads", "install", "enable blockads"}) {
		t.Errorf("node commands = %v, want uninstall (data kept), install, enable again", got)
	}
	if fake.installed.Commit != nodePluginCommit || fake.installed.ManifestSHA256 != nodePluginManifest {
		t.Errorf("installed %+v, want the master's commit and manifest", fake.installed)
	}
	st := statusOf(t, svc, n.Id)
	if st.Plugins == nil || len(st.Plugins.Plugins) != 1 || st.Plugins.Plugins[0].Commit != nodePluginCommit || !st.Plugins.Plugins[0].Enabled {
		t.Errorf("status after the update = %+v", st.Plugins)
	}
}

func TestNodePluginUpdateKeepsADisabledPluginDisabled(t *testing.T) {
	svc, fake := newNodePluginsFixture(t, blockAdsEntry())
	n := readyNode(t)
	onTheNode(fake, func(p *runtime.RemotePlugin) { p.Enabled = false })
	if err := svc.Update(context.Background(), n.Id, "blockads", blockAdsEntry().Permissions); err != nil {
		t.Fatal(err)
	}
	if got := commandsOf(fake); !slices.Equal(got, []string{"uninstall blockads", "install"}) {
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
		onNode func(p *runtime.RemotePlugin)
		absent bool
		want   error
	}{
		{"already on the master's commit", blockAdsEntry(), blockAdsEntry().Permissions, func(p *runtime.RemotePlugin) { p.Commit = nodePluginCommit }, false, ErrNodeUpToDate},
		{"not installed on the node", blockAdsEntry(), blockAdsEntry().Permissions, nil, true, ErrNotOnNode},
		{"installed by hand on the node", blockAdsEntry(), blockAdsEntry().Permissions, func(p *runtime.RemotePlugin) { p.ManagedByMaster = false }, false, ErrNotManagedByMaster},
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
			if err := svc.Update(context.Background(), n.Id, "blockads", tc.perms); !errors.Is(err, tc.want) {
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
	if err := svc.Update(context.Background(), old.Id, "blockads", blockAdsEntry().Permissions); !errors.Is(err, ErrNodeUnsupported) {
		t.Errorf("old node: %v", err)
	}
	if err := svc.Update(context.Background(), down.Id, "blockads", blockAdsEntry().Permissions); !errors.Is(err, ErrNodeUnavailable) {
		t.Errorf("offline node: %v", err)
	}
	if got := fake.callList(); len(got) != 0 {
		t.Errorf("unreachable nodes were contacted: %v", got)
	}
}

type failOn struct {
	*fakeNodePlugins
	failCall string
	err      error
}

func (f *failOn) PluginsUninstall(ctx context.Context, id string, removeData bool) error {
	if f.failCall == "uninstall" {
		f.record("uninstall " + id)
		return f.err
	}
	return f.fakeNodePlugins.PluginsUninstall(ctx, id, removeData)
}

func (f *failOn) PluginsInstall(ctx context.Context, req runtime.RemotePluginInstall) error {
	if f.failCall == "install" {
		f.record("install")
		return f.err
	}
	return f.fakeNodePlugins.PluginsInstall(ctx, req)
}

func (f *failOn) PluginsSetEnabled(ctx context.Context, id string, enabled bool) error {
	if f.failCall == "enable" {
		f.record("enable " + id)
		return f.err
	}
	return f.fakeNodePlugins.PluginsSetEnabled(ctx, id, enabled)
}

func newFailingUpdate(t *testing.T, failCall string) (*NodePluginsService, *failOn, int) {
	t.Helper()
	setupBulkDB(t)
	fake := &failOn{fakeNodePlugins: &fakeNodePlugins{state: runtime.RemotePluginsState{Available: true, AllowMasterManagement: true}}, failCall: failCall, err: errors.New("download failed")}
	onTheNode(fake.fakeNodePlugins, nil)
	svc := NewNodePluginsServiceFor(masterEntries(blockAdsEntry()), func(*model.Node) (NodePluginsClient, error) { return fake, nil })
	return svc, fake, readyNode(t).Id
}

func TestNodePluginUpdateStopsAtTheFirstFailureAndSaysWhatState(t *testing.T) {
	ctx := context.Background()
	perms := blockAdsEntry().Permissions

	svc, fake, id := newFailingUpdate(t, "uninstall")
	if err := svc.Update(ctx, id, "blockads", perms); err == nil || errors.Is(err, ErrNodeUpdateIncomplete) {
		t.Errorf("a refused uninstall = %v, want a plain failure: the old version is still there", err)
	}
	if got := commandsOf(fake.fakeNodePlugins); !slices.Equal(got, []string{"uninstall blockads"}) {
		t.Errorf("commands = %v, want no install after a failed uninstall", got)
	}

	svc, fake, id = newFailingUpdate(t, "install")
	err := svc.Update(ctx, id, "blockads", perms)
	if !errors.Is(err, ErrNodeUpdateIncomplete) || !strings.Contains(err.Error(), "download failed") {
		t.Errorf("a failed install = %v, want ErrNodeUpdateIncomplete with the cause", err)
	}
	if got := commandsOf(fake.fakeNodePlugins); !slices.Equal(got, []string{"uninstall blockads", "install"}) {
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
