// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package agent

import (
	"context"
	"strings"
	"testing"
)

const newerSrc = "#!/bin/sh\necho 3.10.2-lucx.307 with plugins, fixed\n"

// restartRunsAtStart makes a restart do what the panel's unit does: run at-start before the panel starts.
func (e *env) restartRunsAtStart() {
	e.onRestart = func() { _ = e.agent.AtStart(context.Background()) }
}

func (e *env) installedFirstBuild() {
	e.publish(upstreamTag, upstreamTag+"-plugins.1", originalSrc, oursSrc, nil)
	if err := e.agent.AtStart(context.Background()); err != nil || !e.swapped() {
		e.t.Fatalf("first install: %v", err)
	}
}

func TestUpdateSaysThereIsNothingNewerAndLeavesThePanelRunning(t *testing.T) {
	e := newEnv(t)
	e.installedFirstBuild()
	e.restartRunsAtStart()
	res, err := e.agent.Update(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed || res.After != upstreamTag+"-plugins.1" || res.Before != res.After {
		t.Errorf("result = %+v", res)
	}
	if e.restarts != 0 {
		t.Errorf("restarts = %d: nothing was waiting, the panel must not be restarted", e.restarts)
	}
}

func TestUpdateMovesToANewerBuildAndChecksItIsInstalled(t *testing.T) {
	e := newEnv(t)
	e.installedFirstBuild()
	e.publish(upstreamTag, upstreamTag+"-plugins.2", originalSrc, newerSrc, nil)
	e.restartRunsAtStart()
	res, err := e.agent.Update(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || res.Before != upstreamTag+"-plugins.1" || res.After != upstreamTag+"-plugins.2" {
		t.Errorf("result = %+v", res)
	}
	if e.panelSrc() != newerSrc || e.restarts != 1 {
		t.Errorf("panel = %q, restarts = %d", e.panelSrc(), e.restarts)
	}
	if st := e.agent.Status(); st.Pending || st.PendingRelease != "" || st.Release != upstreamTag+"-plugins.2" {
		t.Errorf("state after the update = %+v", st)
	}
}

func TestUpdateInstallsOurBuildOverAnUpstreamOriginal(t *testing.T) {
	e := newEnv(t)
	e.publish(upstreamTag, upstreamTag+"-plugins.1", originalSrc, oursSrc, nil)
	e.restartRunsAtStart()
	res, err := e.agent.Update(context.Background(), true)
	if err != nil || !res.Changed || res.Before != "" || res.After != upstreamTag+"-plugins.1" || !e.swapped() {
		t.Fatalf("result = %+v, err %v, panel %q", res, err, e.panelSrc())
	}
}

func TestUpdateWithoutRestartOnlyGetsTheBuildReady(t *testing.T) {
	e := newEnv(t)
	e.installedFirstBuild()
	e.publish(upstreamTag, upstreamTag+"-plugins.2", originalSrc, newerSrc, nil)
	res, err := e.agent.Update(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed || !res.Waiting || res.After != upstreamTag+"-plugins.2" || e.restarts != 0 || e.panelSrc() != oursSrc {
		t.Errorf("result = %+v, restarts %d, panel %q", res, e.restarts, e.panelSrc())
	}
	if st := e.agent.Status(); !st.Pending || st.PendingRelease != upstreamTag+"-plugins.2" {
		t.Errorf("state = %+v: the waiting build must be recorded", st)
	}
}

func TestUpdateReportsARestartThatDidNotInstallTheBuild(t *testing.T) {
	e := newEnv(t)
	e.installedFirstBuild()
	e.publish(upstreamTag, upstreamTag+"-plugins.2", originalSrc, newerSrc, nil)
	// the restart does not run at-start: the panel stays as it was
	_, err := e.agent.Update(context.Background(), true)
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("err = %v: a restart that left the old build must be reported", err)
	}
}

func TestUpdateReportsABuildThatCannotBeInstalledWithItsReason(t *testing.T) {
	e := newEnv(t)
	e.installedFirstBuild()
	e.publish(upstreamTag, upstreamTag+"-plugins.2", originalSrc, "#!/bin/sh\nexit 3\n", nil)
	e.restartRunsAtStart()
	_, err := e.agent.Update(context.Background(), true)
	if err == nil || !strings.Contains(err.Error(), "does not run") {
		t.Fatalf("err = %v: the reason the build was refused must reach the operator", err)
	}
	if e.panelSrc() != oursSrc {
		t.Errorf("a broken build replaced the panel: %q", e.panelSrc())
	}
}

func TestUpdateRefusesABinaryItDoesNotKnow(t *testing.T) {
	e := newEnv(t)
	e.publish(upstreamTag, upstreamTag+"-plugins.1", originalSrc, oursSrc, nil)
	e.writePanel("#!/bin/sh\necho my own build\n")
	e.restartRunsAtStart()
	_, err := e.agent.Update(context.Background(), true)
	if err == nil || !strings.Contains(err.Error(), "not a known") {
		t.Fatalf("err = %v", err)
	}
	if e.restarts != 0 || e.panelSrc() != "#!/bin/sh\necho my own build\n" {
		t.Errorf("an unknown binary was touched: restarts %d, panel %q", e.restarts, e.panelSrc())
	}
}

func TestUpdateFailsWithoutAList(t *testing.T) {
	e := newEnv(t) // nothing published, nothing cached
	e.restartRunsAtStart()
	_, err := e.agent.Update(context.Background(), true)
	if err == nil {
		t.Fatal("no list can be trusted, yet Update reported success")
	}
	if e.restarts != 0 {
		t.Errorf("restarts = %d", e.restarts)
	}
}

func TestUpdateRefusesAListThatDoesNotVerify(t *testing.T) {
	e := newEnv(t)
	e.installedFirstBuild()
	e.publish(upstreamTag, upstreamTag+"-plugins.2", originalSrc, newerSrc, nil)
	e.serve("/AllIsA1/lucx-plugins/builds/builds.json.sig", []byte("AAAA\n"))
	e.restartRunsAtStart()
	_, err := e.agent.Update(context.Background(), true)
	if err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("err = %v: a list with a bad signature must stop the update", err)
	}
	if e.restarts != 0 || e.panelSrc() != oursSrc {
		t.Errorf("restarts %d, panel %q", e.restarts, e.panelSrc())
	}
}

func TestUpdateForgetsAProblemOfAnEarlierRun(t *testing.T) {
	e := newEnv(t)
	e.restartRunsAtStart()
	if _, err := e.agent.Update(context.Background(), true); err == nil {
		t.Fatal("no list was published yet")
	}
	e.publish(upstreamTag, upstreamTag+"-plugins.1", originalSrc, oursSrc, nil)
	res, err := e.agent.Update(context.Background(), true)
	if err != nil || !res.Changed {
		t.Fatalf("result = %+v, err %v: the old problem must not block a good run", res, err)
	}
	if got := e.agent.Status().LastError; got != "" {
		t.Errorf("LastError = %q after a good run", got)
	}
}
