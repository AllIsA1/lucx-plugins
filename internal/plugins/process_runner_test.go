// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

//go:build linux

package plugins

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// plainSandbox starts plugins as the test user: it exercises the runner
// without needing root.
type plainSandbox struct{}

func (plainSandbox) Check() error                      { return nil }
func (plainSandbox) Isolation() Isolation              { return NewIsolation("plain", false, false, false) }
func (plainSandbox) Own(ProcessSpec, ownedPaths) error { return nil }
func (plainSandbox) Prepare(cmd *exec.Cmd, _ ProcessSpec) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	return nil
}
func (plainSandbox) Signal(pid int, sig syscall.Signal) { _ = syscall.Kill(-pid, sig) }

func newTestProcessRunner(t *testing.T, tune func(*processOptions)) *ProcessRunner {
	t.Helper()
	root := t.TempDir()
	opt := processOptions{
		RuntimeRoot:  filepath.Join(root, "run"),
		StateRoot:    filepath.Join(root, "state"),
		RestartDelay: 5 * time.Millisecond,
		MaxRestarts:  3,
		RestartSpan:  time.Minute,
		StopGrace:    300 * time.Millisecond,
		Sandbox:      plainSandbox{},
	}
	if tune != nil {
		tune(&opt)
	}
	r := NewProcessRunner(opt)
	t.Cleanup(func() { _ = r.StopAll(context.Background()) })
	return r
}

func scriptSpec(t *testing.T, r *ProcessRunner, id, fp, body string) ProcessSpec {
	t.Helper()
	dir := t.TempDir()
	cmd := filepath.Join(dir, "plugin.sh")
	if err := os.WriteFile(cmd, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	socket, data := r.Paths(id)
	return ProcessSpec{ID: id, Fingerprint: fp, CodeDir: dir, Command: cmd, SocketPath: socket, DataDir: data}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func statusOf(t *testing.T, r *ProcessRunner, id string) RunStatus {
	t.Helper()
	st, err := r.Status(context.Background(), id)
	if err != nil {
		t.Fatalf("Status(%s): %v", id, err)
	}
	return st
}

func processAlive(pid int) bool {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	// the state letter follows the closing parenthesis of the command name
	rest := string(raw[strings.LastIndexByte(string(raw), ')')+2:])
	return !strings.HasPrefix(rest, "Z")
}

func logText(t *testing.T, r *ProcessRunner, id string) string {
	t.Helper()
	lines, err := r.Logs(context.Background(), id, 100)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, "\n")
}

func TestProcessRunnerGivesPluginItsEnvironmentAndCredentials(t *testing.T) {
	t.Setenv("XUI_PANEL_SECRET", "leak")
	r := newTestProcessRunner(t, nil)
	token := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(token, []byte("lxp.secret"), 0o400); err != nil {
		t.Fatal(err)
	}
	spec := scriptSpec(t, r, "envy", "fp1", `echo "id=$LUCX_PLUGIN_ID sock=$LUCX_SOCKET data=$LUCX_DATA_DIR state=$STATE_DIRECTORY foo=$FOO leak=${XUI_PANEL_SECRET:-unset} tok=$(cat "$CREDENTIALS_DIRECTORY/panel-token")"
echo "path=${PATH:+set}"
exec sleep 300`)
	spec.TokenFile = token
	spec.Env = map[string]string{"FOO": "bar", "LUCX_PLUGIN_ID": "evil"}
	if err := r.Start(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	eventually(t, "plugin output", func() bool { return strings.Contains(logText(t, r, "envy"), "path=set") })
	want := "id=envy sock=" + spec.SocketPath + " data=" + spec.DataDir + " state=" + spec.DataDir + " foo=bar leak=unset tok=lxp.secret"
	if got := logText(t, r, "envy"); !strings.Contains(got, want) {
		t.Errorf("plugin saw\n%s\nwant a line %q", got, want)
	}
	st := statusOf(t, r, "envy")
	if st.State != StateRunning || st.PID == 0 || st.Fingerprint != "fp1" {
		t.Errorf("status = %+v", st)
	}
}

func TestProcessRunnerStartIsIdempotentPerFingerprint(t *testing.T) {
	r := newTestProcessRunner(t, nil)
	ctx := context.Background()
	spec := scriptSpec(t, r, "idem", "fp1", "exec sleep 300")
	if err := r.Start(ctx, spec); err != nil {
		t.Fatal(err)
	}
	first := statusOf(t, r, "idem").PID
	if err := r.Start(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if got := statusOf(t, r, "idem").PID; got != first {
		t.Errorf("same fingerprint restarted the plugin: pid %d -> %d", first, got)
	}
	spec.Fingerprint = "fp2"
	if err := r.Start(ctx, spec); err != nil {
		t.Fatal(err)
	}
	st := statusOf(t, r, "idem")
	if st.PID == first || st.Fingerprint != "fp2" || st.State != StateRunning {
		t.Errorf("new fingerprint did not replace the process: %+v (old pid %d)", st, first)
	}
	eventually(t, "old process to die", func() bool { return !processAlive(first) })
}

func TestProcessRunnerGivesUpAfterCrashLoop(t *testing.T) {
	r := newTestProcessRunner(t, nil)
	spec := scriptSpec(t, r, "crashy", "fp1", `echo boom
exit 3`)
	if err := r.Start(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	eventually(t, "failed state", func() bool { return statusOf(t, r, "crashy").State == StateFailed })
	st := statusOf(t, r, "crashy")
	if st.Restarts != 3 || !strings.Contains(st.Detail, "status 3") {
		t.Errorf("status = %+v, want 3 restarts and the exit status in detail", st)
	}
	time.Sleep(100 * time.Millisecond)
	if got := statusOf(t, r, "crashy"); got.Restarts != 3 || got.State != StateFailed {
		t.Errorf("a failed plugin kept restarting: %+v", got)
	}
	if n := strings.Count(logText(t, r, "crashy"), "boom"); n != 4 {
		t.Errorf("plugin ran %d times, want 1 start + 3 restarts", n)
	}
}

func TestProcessRunnerRetriesAFailedPluginOnStart(t *testing.T) {
	r := newTestProcessRunner(t, nil)
	spec := scriptSpec(t, r, "later", "fp1", `[ -e "$LUCX_DATA_DIR/ok" ] && exec sleep 300
exit 1`)
	ctx := context.Background()
	if err := r.Start(ctx, spec); err != nil {
		t.Fatal(err)
	}
	eventually(t, "failed state", func() bool { return statusOf(t, r, "later").State == StateFailed })
	if err := os.WriteFile(filepath.Join(spec.DataDir, "ok"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(ctx, spec); err != nil {
		t.Fatal(err)
	}
	eventually(t, "running again", func() bool { return statusOf(t, r, "later").State == StateRunning })
	if got := statusOf(t, r, "later").Restarts; got != 0 {
		t.Errorf("Restarts = %d after a manual start, want the counter reset", got)
	}
}

func TestProcessRunnerStopKillsTheWholeProcessGroup(t *testing.T) {
	r := newTestProcessRunner(t, nil)
	spec := scriptSpec(t, r, "stubborn", "fp1", `trap '' TERM
(trap '' TERM; exec sleep 300) &
echo $! > "$LUCX_DATA_DIR/child.pid"
wait`)
	if err := r.Start(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	var child int
	eventually(t, "grandchild pid", func() bool {
		raw, err := os.ReadFile(filepath.Join(spec.DataDir, "child.pid"))
		if err != nil {
			return false
		}
		child, err = strconv.Atoi(strings.TrimSpace(string(raw)))
		return err == nil
	})
	main := statusOf(t, r, "stubborn").PID
	started := time.Now()
	if err := r.Stop(context.Background(), "stubborn"); err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > 3*time.Second {
		t.Errorf("Stop took %v", time.Since(started))
	}
	eventually(t, "both processes to die", func() bool { return !processAlive(main) && !processAlive(child) })
	if st := statusOf(t, r, "stubborn"); st.State != StateStopped {
		t.Errorf("state after Stop = %q", st.State)
	}
}

func TestProcessRunnerStopRemovesTheTokenFromTheRuntimeDirectory(t *testing.T) {
	r := newTestProcessRunner(t, nil)
	token := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(token, []byte("lxp.secret"), 0o400); err != nil {
		t.Fatal(err)
	}
	spec := scriptSpec(t, r, "tokened", "fp1", "exec sleep 300")
	spec.TokenFile = token
	if err := r.Start(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	copied := filepath.Join(r.credentialsDir("tokened"), tokenFileName)
	if _, err := os.Stat(copied); err != nil {
		t.Fatalf("token copy missing while running: %v", err)
	}
	if err := r.Stop(context.Background(), "tokened"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(copied); !os.IsNotExist(err) {
		t.Errorf("a stopped plugin left its token behind: %v", err)
	}
}

func TestProcessRunnerKillsStragglersWhenThePluginExitsOnItsOwn(t *testing.T) {
	r := newTestProcessRunner(t, nil)
	spec := scriptSpec(t, r, "leaky", "fp1", `sleep 300 &
echo $! > "$LUCX_DATA_DIR/child.pid"
exit 0`)
	if err := r.Start(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	var child int
	eventually(t, "grandchild pid", func() bool {
		raw, err := os.ReadFile(filepath.Join(spec.DataDir, "child.pid"))
		if err != nil {
			return false
		}
		child, err = strconv.Atoi(strings.TrimSpace(string(raw)))
		return err == nil
	})
	eventually(t, "straggler to be killed", func() bool { return !processAlive(child) })
}

func TestProcessRunnerRemovesAStaleSocketBeforeStarting(t *testing.T) {
	r := newTestProcessRunner(t, nil)
	spec := scriptSpec(t, r, "stale", "fp1", "exec sleep 300")
	if err := os.MkdirAll(filepath.Dir(spec.SocketPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spec.SocketPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(spec.SocketPath); !os.IsNotExist(err) {
		t.Errorf("stale socket survived Start: %v", err)
	}
}

func TestProcessRunnerReportsUnknownAndInvalidPlugins(t *testing.T) {
	r := newTestProcessRunner(t, nil)
	ctx := context.Background()
	if st := statusOf(t, r, "nobody"); st.State != StateStopped || st.ID != "nobody" {
		t.Errorf("unknown plugin status = %+v", st)
	}
	if err := r.Stop(ctx, "nobody"); err != nil {
		t.Errorf("Stop of an unknown plugin: %v", err)
	}
	for _, id := range []string{"../etc", "A b", ""} {
		if _, err := r.Status(ctx, id); err == nil {
			t.Errorf("Status(%q) accepted an invalid id", id)
		}
		if err := r.Start(ctx, ProcessSpec{ID: id}); err == nil {
			t.Errorf("Start(%q) accepted an invalid id", id)
		}
	}
}

func TestProcessRunnerListsRunningAndFailedPlugins(t *testing.T) {
	r := newTestProcessRunner(t, nil)
	ctx := context.Background()
	if err := r.Start(ctx, scriptSpec(t, r, "upper", "fp1", "exec sleep 300")); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(ctx, scriptSpec(t, r, "downer", "fp1", "exit 1")); err != nil {
		t.Fatal(err)
	}
	eventually(t, "failed state", func() bool { return statusOf(t, r, "downer").State == StateFailed })
	list, err := r.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]RunState{}
	for _, st := range list {
		got[st.ID] = st.State
	}
	if len(got) != 2 || got["upper"] != StateRunning || got["downer"] != StateFailed {
		t.Errorf("List = %v", got)
	}
}

func TestProcessRunnerStartReportsAnUnstartableBinary(t *testing.T) {
	r := newTestProcessRunner(t, nil)
	spec := scriptSpec(t, r, "broken", "fp1", "true")
	spec.Command = filepath.Join(t.TempDir(), "missing")
	if err := r.Start(context.Background(), spec); err == nil {
		t.Fatal("Start of a missing binary succeeded")
	}
	if st := statusOf(t, r, "broken"); st.State == StateRunning {
		t.Errorf("state = %q", st.State)
	}
}

func TestProcessRunnerStopsPluginsWhenThePanelProcessDies(t *testing.T) {
	kind := os.Getenv("LUCX_TEST_SANDBOX")
	if os.Getenv("LUCX_TEST_PARENT") == "1" {
		runPanelStandIn(kind)
		return
	}
	for _, kind := range []string{"plain", "uid"} {
		t.Run(kind, func(t *testing.T) {
			if kind == "uid" && os.Geteuid() != 0 {
				t.Skip(rootTestHint)
			}
			panelDiesKillsPlugin(t, kind)
		})
	}
}

func panelDiesKillsPlugin(t *testing.T, kind string) {
	dir := publicTempDir(t)
	pidFile := filepath.Join(dir, "state", "orphan", "child.pid")
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessRunnerStopsPluginsWhenThePanelProcessDies$")
	cmd.Env = append(os.Environ(), "LUCX_TEST_PARENT=1", "LUCX_TEST_DIR="+dir, "LUCX_TEST_SANDBOX="+kind)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var child int
	eventually(t, "plugin pid", func() bool {
		raw, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		child, err = strconv.Atoi(strings.TrimSpace(string(raw)))
		return err == nil
	})
	if !processAlive(child) {
		t.Fatal("plugin is not running before the panel dies")
	}
	if err := cmd.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	eventually(t, "plugin to die with the panel", func() bool { return !processAlive(child) })
}

func runPanelStandIn(kind string) {
	dir := os.Getenv("LUCX_TEST_DIR")
	opt := processOptions{RuntimeRoot: filepath.Join(dir, "run"), StateRoot: filepath.Join(dir, "state"), MaxRestarts: 1, RestartSpan: time.Minute, StopGrace: time.Second, Launcher: os.Args[0]}
	if kind == "plain" {
		opt.Sandbox = plainSandbox{}
	}
	r := NewProcessRunner(opt)
	script := filepath.Join(dir, "plugin.sh")
	_ = os.WriteFile(script, []byte("#!/bin/sh\necho $$ > \"$LUCX_DATA_DIR/child.pid\"\nexec sleep 300\n"), 0o755)
	socket, data := r.Paths("orphan")
	if err := r.Start(context.Background(), ProcessSpec{ID: "orphan", Fingerprint: "fp", CodeDir: dir, Command: script, SocketPath: socket, DataDir: data}); err != nil {
		fmt.Fprintln(os.Stderr, "stand-in:", err)
		os.Exit(2)
	}
	select {}
}
