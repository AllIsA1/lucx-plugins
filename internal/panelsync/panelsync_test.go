// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package panelsync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type runner struct {
	mu    sync.Mutex
	calls [][]string
	out   []byte
	err   error
	block chan struct{}
}

func (r *runner) run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	r.calls = append(r.calls, append([]string{name}, args...))
	block := r.block
	r.mu.Unlock()
	if block != nil {
		<-block
	}
	return r.out, r.err
}

func (r *runner) last() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		return nil
	}
	return r.calls[len(r.calls)-1]
}

func setup(t *testing.T, state string) (*Syncer, *runner) {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "lucx-plugins-sync")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if state != "" {
		if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(state), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r := &runner{}
	return New(Config{Exe: exe, StateDir: dir, Run: r.run}), r
}

func TestStatusReadsTheBuildAndTheOneThatWaits(t *testing.T) {
	s, _ := setup(t, `{"release":"v3.10.2-lucx.312-plugins.1","checkedAt":"2026-10-25T10:00:00Z","pending":true,"pendingRelease":"v3.10.2-lucx.314-plugins.1"}`)
	st := s.Status()
	if !st.Managed || st.Release != "v3.10.2-lucx.312-plugins.1" || st.Available != "v3.10.2-lucx.314-plugins.1" || st.CheckedAt.Year() != 2026 || st.Busy || st.Error != "" {
		t.Errorf("status = %+v", st)
	}
}

func TestNothingIsAvailableWhenTheWaitingBuildIsTheOneThatRuns(t *testing.T) {
	s, _ := setup(t, `{"release":"r1","pending":true,"pendingRelease":"r1"}`)
	if a := s.Status().Available; a != "" {
		t.Errorf("available = %q", a)
	}
	s, _ = setup(t, `{"release":"r1","pending":false,"pendingRelease":"r2"}`)
	if a := s.Status().Available; a != "" {
		t.Errorf("a build that is not pending was offered: %q", a)
	}
}

func TestWithoutStateOrToolItIsPlainAboutIt(t *testing.T) {
	s, _ := setup(t, "")
	if st := s.Status(); !st.Managed || st.Release != "" || st.Available != "" {
		t.Errorf("no state: %+v", st)
	}
	none := New(Config{Exe: filepath.Join(t.TempDir(), "missing"), StateDir: t.TempDir()})
	if none.Status().Managed {
		t.Error("a missing tool is managed")
	}
	if err := none.Check(context.Background()); !errors.Is(err, ErrNotManaged) {
		t.Errorf("check = %v", err)
	}
	if err := none.Apply(context.Background()); !errors.Is(err, ErrNotManaged) {
		t.Errorf("apply = %v", err)
	}
}

func TestCheckRunsTheToolAsAChildAndApplyAsATransientUnit(t *testing.T) {
	s, r := setup(t, "")
	if err := s.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := r.last(); len(got) != 2 || got[0] != s.cfg.Exe || got[1] != "check" {
		t.Errorf("check ran %v", got)
	}
	if err := s.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := r.last()
	if got[0] != "systemd-run" || !strings.HasPrefix(strings.Join(got, " "), "systemd-run --pipe --wait --collect --quiet --unit=lucx-plugins-update-") || got[len(got)-2] != s.cfg.Exe || got[len(got)-1] != "update" {
		t.Errorf("apply ran %v: the update must run outside the host's sandbox, as the sync tool's own update", got)
	}
}

func TestAFailureIsReportedInTheToolsWordsAndClearedBySuccess(t *testing.T) {
	s, r := setup(t, "")
	r.out, r.err = []byte("lucx-plugins: no verified builds list is available\n"), errors.New("exit status 1")
	err := s.Apply(context.Background())
	if err == nil || err.Error() != "no verified builds list is available" {
		t.Errorf("err = %v", err)
	}
	if st := s.Status(); st.Error != "no verified builds list is available" || st.Busy {
		t.Errorf("status = %+v", st)
	}
	r.out, r.err = nil, errors.New("exit status 1")
	if err := s.Check(context.Background()); err == nil || err.Error() != "exit status 1" {
		t.Errorf("a silent failure: %v", err)
	}
	r.out, r.err = nil, nil
	if err := s.Check(context.Background()); err != nil || s.Status().Error != "" {
		t.Errorf("after success: %v, %q", err, s.Status().Error)
	}
}

func TestTheToolsOwnLastProblemShowsWhenNothingFailedHere(t *testing.T) {
	s, _ := setup(t, `{"release":"r1","lastError":"cannot download the build"}`)
	if e := s.Status().Error; e != "cannot download the build" {
		t.Errorf("error = %q", e)
	}
}

func TestOnlyOneCheckOrUpdateRunsAtATime(t *testing.T) {
	s, r := setup(t, "")
	r.block = make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- s.Apply(context.Background()) }()
	deadline := time.Now().Add(2 * time.Second)
	for !s.Status().Busy {
		if time.Now().After(deadline) {
			t.Fatal("never busy")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := s.Check(context.Background()); !errors.Is(err, ErrBusy) {
		t.Errorf("a second job: %v", err)
	}
	close(r.block)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if s.Status().Busy {
		t.Error("still busy after the job ended")
	}
}
