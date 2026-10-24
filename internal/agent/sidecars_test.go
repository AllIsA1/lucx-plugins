// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AllIsA1/lucx-plugins/internal/builds"
)

const (
	pinnedFlux = "\x7fELF the openflux the panel release pinned"
	flux13     = "\x7fELF openflux v0.13.0"
	flux14     = "\x7fELF openflux v0.14.0"
	commitFlux = "4f9c786cdd1d9fcd0c133e2e0fec0c5a12522610"
	otherArch  = "\x7fELF openflux v0.13.0 for arm64"
)

func (e *env) binDir() string { return filepath.Join(filepath.Dir(e.panel), "bin") }

func (e *env) fluxPath() string { return filepath.Join(e.binDir(), "openflux-linux-amd64") }

func (e *env) writeFlux(src string) {
	e.t.Helper()
	if err := os.MkdirAll(e.binDir(), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(e.fluxPath(), []byte(src), 0o755); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) fluxSrc() string {
	raw, err := os.ReadFile(e.fluxPath())
	if err != nil {
		return ""
	}
	return string(raw)
}

func releasePath(release string) string {
	return "/" + builds.OwnRepo + "/releases/download/" + release + "/openflux-linux-amd64"
}

// publishFlux signs a sidecar list naming version and serves its binary.
func (e *env) publishFlux(version, body string) {
	e.t.Helper()
	e.publishFluxAs(version, body, e.priv, 0)
}

func (e *env) publishFluxAs(version, body string, key ed25519.PrivateKey, sequenceBump int64) {
	e.t.Helper()
	release := "sidecar-openflux-" + version + "-1"
	idx := &builds.SidecarIndex{Schema: builds.SidecarSchemaVersion, Sequence: e.sidecarSeq + sequenceBump}
	if sequenceBump == 0 {
		e.sidecarSeq++
		idx.Sequence = e.sidecarSeq
	}
	hashes := map[string]string{"amd64": sum([]byte(body)), "arm64": sum([]byte(otherArch))}
	if err := idx.Set("openflux", version, commitFlux, release, hashes); err != nil {
		e.t.Fatal(err)
	}
	idx.Sequence = e.sidecarSeq + sequenceBump
	if sequenceBump == 0 {
		idx.Sequence = e.sidecarSeq
	}
	idx.GeneratedAt = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	raw, err := builds.MarshalSidecars(idx)
	if err != nil {
		e.t.Fatal(err)
	}
	e.serve("/AllIsA1/lucx-plugins/builds/sidecars.json", raw)
	e.serve("/AllIsA1/lucx-plugins/builds/sidecars.json.sig", []byte(builds.SignSidecars(key, raw)+"\n"))
	e.serve(releasePath(release), []byte(body))
}

func TestAtStartInstallsASignedSidecarAndKeepsTheFileItReplaced(t *testing.T) {
	e := newEnv(t)
	e.writeFlux(pinnedFlux)
	e.publishFlux("v0.13.0", flux13)
	if err := e.agent.AtStart(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.fluxSrc() != flux13 {
		t.Fatalf("openflux = %q", e.fluxSrc())
	}
	if raw, _ := os.ReadFile(e.fluxPath() + ".orig"); string(raw) != pinnedFlux {
		t.Errorf(".orig = %q, want the pinned one", raw)
	}
	if info, _ := os.Stat(e.fluxPath()); info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v", info.Mode())
	}
	st := e.agent.Status()
	if got := st.Sidecars["openflux"]; got.SHA256 != sum([]byte(flux13)) || got.Version != "v0.13.0" || st.SidecarError != "" {
		t.Errorf("state = %+v", st)
	}
}

func TestAtStartInstallsASidecarThatIsMissing(t *testing.T) {
	e := newEnv(t)
	if err := os.MkdirAll(e.binDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	e.publishFlux("v0.13.0", flux13)
	_ = e.agent.AtStart(context.Background())
	if e.fluxSrc() != flux13 {
		t.Errorf("openflux = %q", e.fluxSrc())
	}
	if _, err := os.Stat(e.fluxPath() + ".orig"); err == nil {
		t.Error("there was nothing to keep")
	}
}

func TestNoSidecarListIsNotAnError(t *testing.T) {
	e := newEnv(t)
	e.writeFlux(pinnedFlux)
	if err := e.agent.AtStart(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.fluxSrc() != pinnedFlux || e.agent.Status().SidecarError != "" {
		t.Errorf("openflux = %q, state %+v", e.fluxSrc(), e.agent.Status())
	}
}

func TestACurrentSidecarIsNotDownloadedAgain(t *testing.T) {
	e := newEnv(t)
	e.writeFlux(flux13)
	e.publishFlux("v0.13.0", flux13)
	_ = e.agent.AtStart(context.Background())
	if e.hitCount(releasePath("sidecar-openflux-v0.13.0-1")) != 0 {
		t.Error("a file that is already the signed one was downloaded")
	}
	if _, err := os.Stat(e.fluxPath() + ".orig"); err == nil {
		t.Error("a current file was kept as .orig")
	}
	if got := e.agent.Status().Sidecars["openflux"]; got.SHA256 != sum([]byte(flux13)) {
		t.Errorf("state = %+v: a file that matches must be remembered", got)
	}
}

func TestCheckHasTheSidecarReadyAndAtStartInstallsItWithoutDownloadingAgain(t *testing.T) {
	e := newEnv(t)
	e.writeFlux(pinnedFlux)
	e.publishFlux("v0.13.0", flux13)
	if err := e.agent.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.fluxSrc() != pinnedFlux {
		t.Fatal("Check must not replace a file a running panel uses")
	}
	if st := e.agent.Status(); !st.Pending || !strings.Contains(st.PendingRelease, "openflux") {
		t.Errorf("state = %+v, want a pending install", st)
	}
	path := releasePath("sidecar-openflux-v0.13.0-1")
	if e.hitCount(path) != 1 {
		t.Fatalf("downloads = %d", e.hitCount(path))
	}
	_ = e.agent.AtStart(context.Background())
	if e.fluxSrc() != flux13 || e.hitCount(path) != 1 {
		t.Errorf("openflux = %q, downloads = %d", e.fluxSrc(), e.hitCount(path))
	}
}

func TestASidecarWithTheWrongHashIsRefused(t *testing.T) {
	e := newEnv(t)
	e.writeFlux(pinnedFlux)
	e.publishFlux("v0.13.0", flux13)
	e.serve(releasePath("sidecar-openflux-v0.13.0-1"), []byte("\x7fELF something else"))
	_ = e.agent.AtStart(context.Background())
	if e.fluxSrc() != pinnedFlux {
		t.Errorf("openflux = %q", e.fluxSrc())
	}
	if e.agent.Status().SidecarError == "" {
		t.Error("the refusal must be in the state")
	}
}

func TestASidecarThatIsNotAnExecutableIsRefusedEvenWithTheSignedHash(t *testing.T) {
	e := newEnv(t)
	e.writeFlux(pinnedFlux)
	e.publishFlux("v0.13.0", "#!/bin/sh\necho hi\n")
	_ = e.agent.AtStart(context.Background())
	if e.fluxSrc() != pinnedFlux {
		t.Errorf("openflux = %q", e.fluxSrc())
	}
}

func TestASidecarListSignedByAnotherKeyIsRefused(t *testing.T) {
	e := newEnv(t)
	e.writeFlux(pinnedFlux)
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	e.publishFluxAs("v0.13.0", flux13, other, 0)
	_ = e.agent.AtStart(context.Background())
	if e.fluxSrc() != pinnedFlux {
		t.Errorf("openflux = %q", e.fluxSrc())
	}
	if e.hitCount(releasePath("sidecar-openflux-v0.13.0-1")) != 0 {
		t.Error("a binary from an unverified list was downloaded")
	}
}

func TestAnOlderSidecarListIsIgnored(t *testing.T) {
	e := newEnv(t)
	e.writeFlux(pinnedFlux)
	e.publishFlux("v0.13.0", flux13)
	e.publishFlux("v0.14.0", flux14) // sequence 2
	_ = e.agent.AtStart(context.Background())
	if e.fluxSrc() != flux14 {
		t.Fatalf("openflux = %q", e.fluxSrc())
	}
	// someone serves the first list again
	e.sidecarSeq = 0
	e.publishFlux("v0.13.0", flux13)
	_ = e.agent.AtStart(context.Background())
	if e.fluxSrc() != flux14 {
		t.Errorf("openflux = %q: the agent went back to an older list", e.fluxSrc())
	}
}

func TestLaterSidecarBuildsKeepTheFirstOriginal(t *testing.T) {
	e := newEnv(t)
	e.writeFlux(pinnedFlux)
	e.publishFlux("v0.13.0", flux13)
	_ = e.agent.AtStart(context.Background())
	e.publishFlux("v0.14.0", flux14)
	_ = e.agent.AtStart(context.Background())
	if e.fluxSrc() != flux14 {
		t.Fatalf("openflux = %q", e.fluxSrc())
	}
	if raw, _ := os.ReadFile(e.fluxPath() + ".orig"); string(raw) != pinnedFlux {
		t.Errorf(".orig = %q: it must stay the file the panel release came with", raw)
	}
}

func TestSidecarsOffLeavesThemAloneWithoutAskingTheNetwork(t *testing.T) {
	e := newEnv(t)
	e.agent.cfg.NoSidecars = true
	e.writeFlux(pinnedFlux)
	e.publishFlux("v0.13.0", flux13)
	_ = e.agent.AtStart(context.Background())
	if e.fluxSrc() != pinnedFlux || e.hitCount("/AllIsA1/lucx-plugins/builds/sidecars.json") != 0 {
		t.Errorf("openflux = %q, list requests = %d", e.fluxSrc(), e.hitCount("/AllIsA1/lucx-plugins/builds/sidecars.json"))
	}
}

func TestWithoutABinFolderTheAgentLeavesSidecarsAlone(t *testing.T) {
	e := newEnv(t)
	e.publishFlux("v0.13.0", flux13)
	_ = e.agent.AtStart(context.Background())
	if _, err := os.Stat(e.binDir()); err == nil {
		t.Error("the agent made a bin folder in a layout it does not know")
	}
}

func TestRestorePutsTheReplacedSidecarBack(t *testing.T) {
	e := newEnv(t)
	e.writeFlux(pinnedFlux)
	e.publishFlux("v0.13.0", flux13)
	_ = e.agent.AtStart(context.Background())
	changed, err := e.agent.Restore(context.Background())
	if err != nil || !changed {
		t.Fatalf("changed = %v, err = %v", changed, err)
	}
	if e.fluxSrc() != pinnedFlux {
		t.Errorf("openflux = %q", e.fluxSrc())
	}
	if _, err := os.Stat(e.fluxPath() + ".orig"); err == nil {
		t.Error(".orig is left behind")
	}
	if _, ok := e.agent.Status().Sidecars["openflux"]; ok {
		t.Error("the state still says the agent owns the file")
	}
}

func TestRestoreLeavesASidecarThePersonReplacedThemselves(t *testing.T) {
	e := newEnv(t)
	e.writeFlux(pinnedFlux)
	e.publishFlux("v0.13.0", flux13)
	_ = e.agent.AtStart(context.Background())
	e.writeFlux("\x7fELF uploaded on Settings, Cores")
	if changed, _ := e.agent.Restore(context.Background()); changed {
		t.Error("restore touched a file the agent did not install")
	}
	if e.fluxSrc() != "\x7fELF uploaded on Settings, Cores" {
		t.Errorf("openflux = %q", e.fluxSrc())
	}
}

func TestUpdateRestartsForASidecarAloneAndChecksItWasInstalled(t *testing.T) {
	e := newEnv(t)
	e.installedFirstBuild()
	e.writeFlux(pinnedFlux)
	e.publishFlux("v0.13.0", flux13)
	e.restartRunsAtStart()
	res, err := e.agent.Update(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || len(res.Sidecars) != 1 || res.Sidecars[0] != "openflux" || res.After != res.Before {
		t.Errorf("result = %+v", res)
	}
	if e.restarts != 1 || e.fluxSrc() != flux13 {
		t.Errorf("restarts = %d, openflux = %q", e.restarts, e.fluxSrc())
	}
}

func TestUpdateWithoutRestartOnlyGetsTheSidecarReady(t *testing.T) {
	e := newEnv(t)
	e.installedFirstBuild()
	e.writeFlux(pinnedFlux)
	e.publishFlux("v0.13.0", flux13)
	res, err := e.agent.Update(context.Background(), false)
	if err != nil || !res.Waiting || len(res.Sidecars) != 1 || e.restarts != 0 || e.fluxSrc() != pinnedFlux {
		t.Errorf("result = %+v, err = %v, restarts = %d, openflux = %q", res, err, e.restarts, e.fluxSrc())
	}
}

func TestUpdateReportsARestartThatLeftTheSidecarOld(t *testing.T) {
	e := newEnv(t)
	e.installedFirstBuild()
	e.writeFlux(pinnedFlux)
	e.publishFlux("v0.13.0", flux13)
	_, err := e.agent.Update(context.Background(), true) // the restart does not run at-start
	if err == nil || !strings.Contains(err.Error(), "openflux") {
		t.Errorf("err = %v, want the sidecar named", err)
	}
}

func TestUpdateWithNothingNewForSidecarsRestartsNothing(t *testing.T) {
	e := newEnv(t)
	e.installedFirstBuild()
	e.writeFlux(flux13)
	e.publishFlux("v0.13.0", flux13)
	e.restartRunsAtStart()
	res, err := e.agent.Update(context.Background(), true)
	if err != nil || res.Changed || len(res.Sidecars) != 0 || e.restarts != 0 {
		t.Errorf("result = %+v, err = %v, restarts = %d", res, err, e.restarts)
	}
}

func TestAFileTheAgentInstalledIsNeverKeptAsTheOriginal(t *testing.T) {
	e := newEnv(t)
	e.writeFlux(pinnedFlux)
	e.publishFlux("v0.13.0", flux13)
	_ = e.agent.AtStart(context.Background())
	if err := os.Remove(e.fluxPath() + ".orig"); err != nil {
		t.Fatal(err)
	}
	e.publishFlux("v0.14.0", flux14)
	_ = e.agent.AtStart(context.Background())
	if raw, err := os.ReadFile(e.fluxPath() + ".orig"); err == nil {
		t.Errorf(".orig = %q: our own earlier build is not an original", raw)
	}
}
