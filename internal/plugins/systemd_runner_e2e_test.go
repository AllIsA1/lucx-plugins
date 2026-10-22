// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const e2eScript = `#!/bin/sh
echo "ready id=$LUCX_PLUGIN_ID"
touch "$PWD/probe" 2>/dev/null && echo "code-write:allowed" || echo "code-write:denied"
touch "$STATE_DIRECTORY/probe" 2>/dev/null && echo "state-write:allowed" || echo "state-write:denied"
ls /tmp/lucx-e2e-sentinel >/dev/null 2>&1 && echo "tmp:shared" || echo "tmp:private"
exec sleep 300
`

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// Needs a user systemd manager; run with XUI_PLUGINS_SYSTEMD_E2E=1. It starts
// real units but cannot cover DynamicUser, the IP filter or ProtectSystem on its own.
func TestSystemdRunnerUserModeE2E(t *testing.T) {
	if os.Getenv("XUI_PLUGINS_SYSTEMD_E2E") != "1" {
		t.Skip("set XUI_PLUGINS_SYSTEMD_E2E=1 to run against the user systemd manager")
	}
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	home, err := os.UserHomeDir()
	if err != nil || runtimeDir == "" {
		t.Skip("no user session")
	}
	r := newUserSystemdRunner(filepath.Join(runtimeDir, "x-ui-plugins"), filepath.Join(home, ".local", "state", "x-ui-plugins"))
	if err := r.Available(); err != nil {
		t.Skipf("user systemd unavailable: %v", err)
	}

	var rnd [3]byte
	_, _ = rand.Read(rnd[:])
	id := "lucx-e2e-" + hex.EncodeToString(rnd[:])
	codeDir, err := os.MkdirTemp(filepath.Join(home, ".cache"), "lucx-e2e-code-")
	if err != nil {
		t.Skipf("cannot create a code dir under ~/.cache: %v", err)
	}
	script := filepath.Join(codeDir, "plugin.sh")
	if err := os.WriteFile(script, []byte(e2eScript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("/tmp/lucx-e2e-sentinel", []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	socket, data := r.Paths(id)
	t.Cleanup(func() {
		_ = r.Stop(context.Background(), id)
		_ = os.RemoveAll(codeDir)
		_ = os.RemoveAll(data)
		_ = os.Remove("/tmp/lucx-e2e-sentinel")
	})
	ctx := context.Background()
	spec := ProcessSpec{ID: id, Fingerprint: "fp1", CodeDir: codeDir, Command: script, SocketPath: socket, DataDir: data}

	if err := r.Start(ctx, spec); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, "running", func() bool { st, _ := r.Status(ctx, id); return st.State == StateRunning })
	st, _ := r.Status(ctx, id)
	if st.Fingerprint != "fp1" || st.PID == 0 {
		t.Fatalf("status = %+v", st)
	}

	waitFor(t, "sandbox report in the journal", func() bool {
		lines, _ := r.Logs(ctx, id, 50)
		return slices.Contains(lines, "tmp:private")
	})
	lines, _ := r.Logs(ctx, id, 50)
	for _, want := range []string{"ready id=" + id, "code-write:denied", "state-write:allowed", "tmp:private"} {
		if !slices.Contains(lines, want) {
			t.Errorf("sandbox report lacks %q; got %q", want, lines)
		}
	}

	if err := r.Start(ctx, spec); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if again, _ := r.Status(ctx, id); again.PID != st.PID {
		t.Errorf("Start with the same fingerprint restarted the process: pid %d -> %d", st.PID, again.PID)
	}

	listed, err := r.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !slices.ContainsFunc(listed, func(s RunStatus) bool { return s.ID == id && s.State == StateRunning }) {
		t.Errorf("List = %+v does not show %s running", listed, id)
	}

	spec.Fingerprint = "fp2"
	if err := r.Start(ctx, spec); err != nil {
		t.Fatalf("Start with a new fingerprint: %v", err)
	}
	waitFor(t, "restart with the new fingerprint", func() bool {
		s, _ := r.Status(ctx, id)
		return s.State == StateRunning && s.Fingerprint == "fp2" && s.PID != st.PID
	})

	if err := r.Stop(ctx, id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	waitFor(t, "stopped", func() bool { s, _ := r.Status(ctx, id); return s.State == StateStopped })
	if err := r.Stop(ctx, id); err != nil {
		t.Errorf("stopping a stopped plugin must not fail: %v", err)
	}
}

func TestSystemdRunnerUserModeReportsExecFailure(t *testing.T) {
	if os.Getenv("XUI_PLUGINS_SYSTEMD_E2E") != "1" {
		t.Skip("set XUI_PLUGINS_SYSTEMD_E2E=1 to run against the user systemd manager")
	}
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	home, err := os.UserHomeDir()
	if err != nil || runtimeDir == "" {
		t.Skip("no user session")
	}
	r := newUserSystemdRunner(filepath.Join(runtimeDir, "x-ui-plugins"), filepath.Join(home, ".local", "state", "x-ui-plugins"))
	if err := r.Available(); err != nil {
		t.Skipf("user systemd unavailable: %v", err)
	}
	spec := ProcessSpec{ID: "lucx-e2e-missing", Fingerprint: "fp", CodeDir: home, Command: filepath.Join(home, "no-such-binary")}
	t.Cleanup(func() { _ = r.Stop(context.Background(), spec.ID) })
	err = r.Start(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "no-such-binary") {
		t.Fatalf("Start of a missing binary: err = %v, want an error naming it (Type=exec)", err)
	}
}
