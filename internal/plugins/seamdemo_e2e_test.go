// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// demoManifest reads the example's own manifest so the test follows it.
func demoManifest(t *testing.T) (perms []string, seams []map[string]any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "examples", "seam-demo", ManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseManifest(raw); err != nil {
		t.Fatalf("the example manifest is not valid: %v", err)
	}
	var m struct {
		Permissions []string         `json:"permissions"`
		Seams       []map[string]any `json:"seams"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m.Permissions, m.Seams
}

func TestSeamDemoManifestAsksForWhatItUses(t *testing.T) {
	perms, seams := demoManifest(t)
	if len(seams) != 3 {
		t.Errorf("seams = %v", seams)
	}
	for _, p := range perms {
		if !IsDangerousPermission(p) {
			t.Errorf("%s must be shown to the operator as dangerous", p)
		}
	}
}

// The real example binary behind the real dispatcher: what the SDK writes, the panel's side understands.
func TestSeamDemoThroughTheDispatcher(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not found")
	}
	example, err := filepath.Abs(filepath.Join("..", "..", "examples", "seam-demo"))
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "seamdemo")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = example
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the example: %v\n%s", err, out)
	}
	perms, seams := demoManifest(t)
	sockDir, err := os.MkdirTemp("", "lucxd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	e := newSupEnv(t, func(m map[string]any) { m["permissions"] = perms; m["seams"] = seams })
	t.Cleanup(e.sup.Close)
	e.runner.socketDir = sockDir
	e.enable(t)
	e.reconcile()

	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(), "LUCX_SOCKET="+filepath.Join(sockDir, "traffic-alerts.sock"))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(sockDir, "traffic-alerts.sock")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the example did not start listening")
		}
		time.Sleep(20 * time.Millisecond)
	}
	ctx := context.Background()
	status := SeamRequest{Engine: SeamEngineAPI, Method: "GET", Route: "/server/status", Path: "/panel/api/server/status"}

	tea := status
	tea.Query = "teapot=1"
	if got := e.sup.SeamPre(ctx, tea); !got.Responded || got.Response.Status != 418 || !strings.Contains(string(got.Response.Body), "teapot") || got.Failures != 0 {
		t.Errorf("the plugin did not answer the teapot request: %+v", got)
	}
	if got := e.sup.SeamPre(ctx, status); got.Responded || got.Failures != 0 {
		t.Errorf("an ordinary status request must go on to the panel: %+v", got)
	}
	post := e.sup.SeamPost(ctx, status, SeamResponse{Status: 200, Headers: map[string][]string{"Content-Type": {"application/json"}}, Body: []byte(`{"success":true}`)})
	if !post.Changed || post.Response.Headers["X-Seam-Demo"][0] != "1" || string(post.Response.Body) != `{"success":true}` || post.Response.Status != 200 {
		t.Errorf("the status answer was not marked, or was damaged: %+v", post)
	}

	sub := SeamRequest{Engine: SeamEngineSub, Method: "GET", Route: "/links/:subid", Path: "/sub/abc"}
	links := base64.StdEncoding.EncodeToString([]byte("vless://a#one"))
	post = e.sup.SeamPost(ctx, sub, SeamResponse{Status: 200, Body: []byte(links)})
	decoded, err := base64.StdEncoding.DecodeString(string(post.Response.Body))
	if err != nil || !post.Changed || !strings.HasPrefix(string(decoded), "vless://a#one\n#") {
		t.Errorf("the subscription line was not added: %+v %q %v", post, decoded, err)
	}
	if rt, _ := e.sup.Runtime("traffic-alerts"); rt.SeamCalls < 4 || rt.SeamErrors != 0 {
		t.Errorf("counters = %d calls, %d errors", rt.SeamCalls, rt.SeamErrors)
	}
}
