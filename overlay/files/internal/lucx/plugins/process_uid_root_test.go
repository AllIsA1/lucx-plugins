// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

//go:build linux

package plugins

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// These tests need root. As an ordinary user run the compiled test binary in a
// user namespace that maps a uid range (util-linux 2.38+):
//
//	go test -c -o plugins.test ./internal/lucx/plugins
//	unshare -U --map-root-user --map-users=auto --map-groups=auto ./plugins.test -test.run 'Root|PanelProcessDies' -test.v
const rootTestHint = "needs root; run the test binary in `unshare -U --map-root-user --map-users=auto --map-groups=auto`"

func publicTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "lucx-plugin-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func newRootRunner(t *testing.T) (*ProcessRunner, string) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip(rootTestHint)
	}
	root := publicTempDir(t)
	r := NewProcessRunner(processOptions{
		RuntimeRoot: filepath.Join(root, "run"), StateRoot: filepath.Join(root, "state"),
		RestartDelay: 5 * time.Millisecond, MaxRestarts: 1, StopGrace: 300 * time.Millisecond,
		Launcher: os.Args[0],
	})
	t.Cleanup(func() { _ = r.StopAll(context.Background()) })
	return r, root
}

func rootScriptSpec(t *testing.T, r *ProcessRunner, root, id, body string) ProcessSpec {
	t.Helper()
	dir := filepath.Join(root, "code-"+id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := filepath.Join(dir, "plugin.sh")
	if err := os.WriteFile(cmd, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	socket, data := r.Paths(id)
	return ProcessSpec{ID: id, Fingerprint: "fp1", CodeDir: dir, Command: cmd, SocketPath: socket, DataDir: data}
}

func TestRootProcessRunnerConfinesThePlugin(t *testing.T) {
	r, root := newRootRunner(t)
	secret := filepath.Join(root, "panel-secret")
	if err := os.WriteFile(secret, []byte("hunter2"), 0o600); err != nil {
		t.Fatal(err)
	}
	token := filepath.Join(root, "token")
	if err := os.WriteFile(token, []byte("lxp.abc"), 0o400); err != nil {
		t.Fatal(err)
	}
	spec := rootScriptSpec(t, r, root, "confined", `echo "uid=$(id -u) gid=$(id -g) groups=$(id -G)"
grep -E '^(NoNewPrivs|CapBnd|CapEff|CapPrm|CapAmb):' /proc/self/status | tr -s '\t ' ' '
echo "nofile=$(ulimit -n) core=$(ulimit -c) nproc=$(grep 'Max processes' /proc/self/limits | tr -s ' ' | cut -d' ' -f3) data=$(ulimit -d) umask=$(umask)"
cat "$SECRET" >/dev/null 2>&1 && echo secret=readable || echo secret=denied
echo x > "$LUCX_DATA_DIR/w" && echo data=writable
echo x > "$(dirname "$0")/x" 2>/dev/null && echo code=writable || echo code=readonly
echo "token=$(cat "$CREDENTIALS_DIRECTORY/panel-token") owner=$(stat -c '%u:%a' "$CREDENTIALS_DIRECTORY/panel-token")"
echo ready
exec sleep 300`)
	spec.TokenFile = token
	spec.Env = map[string]string{"SECRET": secret}
	if err := r.Start(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	eventually(t, "plugin report", func() bool { return strings.Contains(logText(t, r, "confined"), "ready") })
	got := logText(t, r, "confined")
	for _, want := range []string{
		"uid=40000 gid=40000 groups=40000",
		"NoNewPrivs: 1", "CapBnd: 0000000000000000", "CapEff: 0000000000000000", "CapPrm: 0000000000000000", "CapAmb: 0000000000000000",
		"nofile=4096 core=0 nproc=64 data=262144 umask=0077",
		"secret=denied", "data=writable", "code=readonly",
		"token=lxp.abc owner=40000:400",
	} {
		if !slices.Contains(strings.Split(got, "\n"), want) {
			t.Errorf("plugin report lacks the line %q:\n%s", want, got)
		}
	}
	if st := statusOf(t, r, "confined"); st.State != StateRunning {
		t.Errorf("state = %q (%s)", st.State, st.Detail)
	}
}

func TestRootProcessRunnerGivesEachPluginItsOwnUser(t *testing.T) {
	r, root := newRootRunner(t)
	ctx := context.Background()
	first := rootScriptSpec(t, r, root, "first", `echo "uid=$(id -u)" > "$LUCX_DATA_DIR/who"
echo ready
exec sleep 300`)
	if err := r.Start(ctx, first); err != nil {
		t.Fatal(err)
	}
	eventually(t, "first plugin", func() bool { return strings.Contains(logText(t, r, "first"), "ready") })
	_, firstData := r.Paths("first")
	second := rootScriptSpec(t, r, root, "second", `echo "uid=$(id -u)"
cat "$OTHER/who" >/dev/null 2>&1 && echo other=readable || echo other=denied
ls "$OTHER" >/dev/null 2>&1 && echo list=allowed || echo list=denied
echo ready
exec sleep 300`)
	second.Env = map[string]string{"OTHER": firstData}
	if err := r.Start(ctx, second); err != nil {
		t.Fatal(err)
	}
	eventually(t, "second plugin", func() bool { return strings.Contains(logText(t, r, "second"), "ready") })
	got := logText(t, r, "second")
	for _, want := range []string{"uid=40001", "other=denied", "list=denied"} {
		if !strings.Contains(got, want) {
			t.Errorf("second plugin report lacks %q:\n%s", want, got)
		}
	}
}

func TestRootProcessRunnerKeepsAPluginsUserAcrossRunners(t *testing.T) {
	r, root := newRootRunner(t)
	if err := r.Start(context.Background(), rootScriptSpec(t, r, root, "steady", `id -u; exec sleep 300`)); err != nil {
		t.Fatal(err)
	}
	if err := r.Stop(context.Background(), "steady"); err != nil {
		t.Fatal(err)
	}
	again := NewProcessRunner(processOptions{RuntimeRoot: r.opt.RuntimeRoot, StateRoot: r.opt.StateRoot, Launcher: os.Args[0], MaxRestarts: 1})
	t.Cleanup(func() { _ = again.StopAll(context.Background()) })
	if err := again.Start(context.Background(), rootScriptSpec(t, again, root, "steady", `id -u; exec sleep 300`)); err != nil {
		t.Fatal(err)
	}
	eventually(t, "uid line", func() bool { return strings.Contains(logText(t, again, "steady"), "40000") })
}

func TestRootProcessRunnerRefusesWhatItCannotConfine(t *testing.T) {
	r, root := newRootRunner(t)
	closed := rootScriptSpec(t, r, root, "closed", "exec sleep 300")
	if err := os.Chmod(closed.CodeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(context.Background(), closed); err == nil || !strings.Contains(err.Error(), closed.CodeDir) {
		t.Errorf("Start with an unsearchable code dir = %v, want an error naming %s", err, closed.CodeDir)
	}
	foreign := rootScriptSpec(t, r, root, "foreign", "exec sleep 300")
	if err := os.MkdirAll(foreign.DataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(foreign.DataDir, 50000, 50000); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(context.Background(), foreign); err == nil || !strings.Contains(err.Error(), "owned by user 50000") {
		t.Errorf("Start with a data dir owned by another user = %v, want the ownership error", err)
	}
}

func TestProcessRunnerAvailabilityNeedsRoot(t *testing.T) {
	r := NewProcessRunner(processOptions{RuntimeRoot: t.TempDir(), StateRoot: t.TempDir(), Launcher: os.Args[0]})
	err := r.Available()
	if os.Geteuid() != 0 {
		if err == nil || !strings.Contains(err.Error(), "root") {
			t.Errorf("Available as an ordinary user = %v, want the needs-root error", err)
		}
		return
	}
	if err != nil {
		t.Errorf("Available as root = %v", err)
	}
	if iso := r.Isolation(); iso.Level != IsolationPartial || !iso.User || iso.Network {
		t.Errorf("Isolation = %+v", iso)
	}
}
