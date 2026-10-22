// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type systemFixture struct {
	t     *testing.T
	ctrl  *SystemControl
	state string
	calls [][]string
	out   map[string]string // unit -> "systemctl is-active" output
	fail  error
}

func newSystemFixture(t *testing.T) *systemFixture {
	t.Helper()
	dir := t.TempDir()
	f := &systemFixture{t: t, state: filepath.Join(dir, "state"), out: map[string]string{}}
	units := filepath.Join(dir, "units")
	for _, p := range []string{f.state, units} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	conf := filepath.Join(dir, "config")
	if err := os.WriteFile(conf, []byte("# written by the installer\nPANEL_BINARY=/usr/local/x-ui/x-ui\nSTATE_DIR="+f.state+"\nRESTART_AT=05:30\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(units, applyUnit), []byte("[Service]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.ctrl = &SystemControl{
		ConfigPath: conf,
		UnitDir:    units,
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			f.calls = append(f.calls, append([]string{name}, args...))
			if len(args) >= 2 && args[0] == "is-active" {
				return []byte(f.out[args[1]] + "\n"), errors.New("exit status 3")
			}
			return []byte("boom output"), f.fail
		},
	}
	return f
}

func (f *systemFixture) writeState(doc string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.state, "state.json"), []byte(doc), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *systemFixture) started() []string {
	var units []string
	for _, c := range f.calls {
		if len(c) >= 4 && c[0] == "systemctl" && c[1] == "start" && c[2] == "--no-block" {
			units = append(units, c[3])
		}
	}
	return units
}

func TestSystemStatusReadsTheAgentsState(t *testing.T) {
	f := newSystemFixture(t)
	f.writeState(`{"maxSequence":6,"original":"aa","ours":"bb","upstream":"v3.10.2-lucx.307","release":"v3.10.2-lucx.307-plugins.3",
		"swappedAt":"2026-10-22T18:04:11Z","checkedAt":"2026-10-22T19:00:00Z","pending":true,"lastError":"the published builds list is refused"}`)
	st := f.ctrl.Status(context.Background())
	if !st.Managed || st.Upstream != "v3.10.2-lucx.307" || st.Release != "v3.10.2-lucx.307-plugins.3" || !st.Pending {
		t.Fatalf("status = %+v", st)
	}
	if st.RestartAt != "05:30" || st.LastError != "the published builds list is refused" {
		t.Errorf("restart hour / last error = %q / %q", st.RestartAt, st.LastError)
	}
	if st.SwappedAt == nil || !st.SwappedAt.Equal(time.Date(2026, 10, 22, 18, 4, 11, 0, time.UTC)) || st.CheckedAt == nil {
		t.Errorf("times = %v %v", st.SwappedAt, st.CheckedAt)
	}
}

func TestSystemStatusCopesWithMissingOrDamagedState(t *testing.T) {
	f := newSystemFixture(t)
	if st := f.ctrl.Status(context.Background()); !st.Managed || st.Release != "" || st.SwappedAt != nil || st.RestartAt != "05:30" {
		t.Errorf("before the program ever ran: %+v", st)
	}
	f.writeState(`{"upstream":"v3.10.2-lucx.307","pending":false}`) // an older program writes no release
	if st := f.ctrl.Status(context.Background()); st.Upstream != "v3.10.2-lucx.307" || st.Release != "" {
		t.Errorf("a state without a release: %+v", st)
	}
	f.writeState("{broken")
	if st := f.ctrl.Status(context.Background()); !st.Managed || !strings.Contains(st.LastError, "unreadable") {
		t.Errorf("a damaged state must be reported, not crash: %+v", st)
	}
}

func TestSystemIsNotManagedWithoutTheInstaller(t *testing.T) {
	f := newSystemFixture(t)
	if err := os.Remove(f.ctrl.ConfigPath); err != nil {
		t.Fatal(err)
	}
	if st := f.ctrl.Status(context.Background()); st.Managed || st.Reason != "not-installed" {
		t.Errorf("no configuration: %+v", st)
	}
	f = newSystemFixture(t)
	if err := os.Remove(filepath.Join(f.ctrl.UnitDir, applyUnit)); err != nil {
		t.Fatal(err)
	}
	if st := f.ctrl.Status(context.Background()); st.Managed || st.Reason != "units-missing" {
		t.Errorf("no units: %+v", st)
	}
	for name, call := range map[string]func() error{
		"check":  func() error { return f.ctrl.Check(context.Background()) },
		"update": func() error { return f.ctrl.Update(context.Background()) },
	} {
		if err := call(); !errors.Is(err, ErrSystemNotManaged) {
			t.Errorf("%s: %v, want ErrSystemNotManaged", name, err)
		}
	}
	if len(f.started()) != 0 {
		t.Errorf("a host without the installer must not run systemctl: %v", f.calls)
	}
}

func TestSystemCheckAndUpdateStartTheInstalledUnitsWithoutWaiting(t *testing.T) {
	f := newSystemFixture(t)
	if err := f.ctrl.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := f.ctrl.Update(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := f.started()
	if len(got) != 2 || got[0] != "lucx-plugins-sync.service" || got[1] != "lucx-plugins-apply.service" {
		t.Errorf("started units = %v; --no-block matters: an update restarts the panel that asked for it", got)
	}
}

func TestSystemRefusesToStartWhileAJobRuns(t *testing.T) {
	f := newSystemFixture(t)
	f.out["lucx-plugins-apply.service"] = "activating"
	if st := f.ctrl.Status(context.Background()); !st.Busy {
		t.Errorf("a running job must show as busy: %+v", st)
	}
	if err := f.ctrl.Update(context.Background()); !errors.Is(err, ErrSystemBusy) {
		t.Errorf("Update while busy: %v", err)
	}
	if len(f.started()) != 0 {
		t.Errorf("a second job was started: %v", f.started())
	}
	f.out["lucx-plugins-apply.service"] = "inactive"
	if st := f.ctrl.Status(context.Background()); st.Busy {
		t.Errorf("an idle host reads as busy: %+v", st)
	}
}

func TestSystemReportsWhyAStartFailed(t *testing.T) {
	f := newSystemFixture(t)
	f.fail = errors.New("exit status 1")
	err := f.ctrl.Update(context.Background())
	if err == nil || !strings.Contains(err.Error(), "boom output") {
		t.Errorf("the systemctl output must reach the log: %v", err)
	}
}
