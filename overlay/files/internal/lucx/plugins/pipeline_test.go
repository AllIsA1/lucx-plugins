// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const pipelineDoc = `{"routing":{"rules":[]},"outbounds":[{"tag":"direct"}]}`

func patchReply(patch string) func(http.ResponseWriter, *http.Request, []byte) {
	return func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"patch":` + patch + `}`))
	}
}

func pipelineEnv(t *testing.T, handler func(http.ResponseWriter, *http.Request, []byte), opts ...func(*SupervisorOptions)) (*supEnv, *pluginSocket) {
	t.Helper()
	return eventEnv(t, []string{"pipeline:xray.config"}, handler, opts...)
}

func (e *supEnv) transform(doc string, validate func(before, after []byte) error) (string, []PipelineResult) {
	out, res := e.sup.Transform(context.Background(), PipelineXrayConfig, []byte(doc), validate)
	return string(out), res
}

func TestTransformAppliesAPluginsPatch(t *testing.T) {
	e, plug := pipelineEnv(t, patchReply(`[{"op":"add","path":"/routing/rules/-","value":{"outboundTag":"direct"}}]`))
	got, res := e.transform(pipelineDoc, nil)

	if canon(t, []byte(got)) != canon(t, []byte(`{"routing":{"rules":[{"outboundTag":"direct"}]},"outbounds":[{"tag":"direct"}]}`)) {
		t.Errorf("result = %s", got)
	}
	if len(res) != 1 || res[0].Plugin != "traffic-alerts" || res[0].Applied != 1 || res[0].Err != nil {
		t.Errorf("results = %+v", res)
	}
	reqs := plug.requests("/_lucx/pipeline/xray.config")
	if len(reqs) != 1 || reqs[0].Method != http.MethodPost || reqs[0].Body != pipelineDoc {
		t.Fatalf("the plugin must receive the current document verbatim: %+v", reqs)
	}
	if reqs[0].Header.Get("X-Lucx-Pipeline") != "xray.config" {
		t.Errorf("headers = %v", reqs[0].Header)
	}
}

func TestTransformIsFailOpen(t *testing.T) {
	cases := map[string]func(http.ResponseWriter, *http.Request, []byte){
		"http error": func(w http.ResponseWriter, _ *http.Request, _ []byte) {
			http.Error(w, "boom", http.StatusInternalServerError)
		},
		"not json":       func(w http.ResponseWriter, _ *http.Request, _ []byte) { _, _ = w.Write([]byte("<html>")) },
		"unsupported op": patchReply(`[{"op":"move","from":"/a","path":"/b"}]`),
		"failing op":     patchReply(`[{"op":"remove","path":"/does/not/exist"}]`),
		"too many ops":   patchReply("[" + strings.Repeat(`{"op":"add","path":"/x","value":1},`, MaxPatchOps) + `{"op":"add","path":"/x","value":1}]`),
		"slow plugin": func(w http.ResponseWriter, _ *http.Request, _ []byte) {
			time.Sleep(400 * time.Millisecond)
			patchReply(`[{"op":"add","path":"/late","value":1}]`)(w, nil, nil)
		},
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			e, _ := pipelineEnv(t, handler, func(o *SupervisorOptions) { o.PipelineTimeout = 100 * time.Millisecond })
			got, res := e.transform(pipelineDoc, nil)
			if canon(t, []byte(got)) != canon(t, []byte(pipelineDoc)) {
				t.Errorf("a misbehaving plugin changed the config: %s", got)
			}
			if len(res) != 1 || res[0].Err == nil || res[0].Applied != 0 {
				t.Errorf("results = %+v, want one recorded error", res)
			}
			rt, _ := e.sup.Runtime("traffic-alerts")
			if rt.PipelineErrors != 1 || rt.LastError == "" {
				t.Errorf("the error must be visible in the runtime: %+v", rt)
			}
		})
	}
}

func TestTransformRevertsAPatchTheValidatorRefuses(t *testing.T) {
	e, _ := pipelineEnv(t, patchReply(`[{"op":"remove","path":"/outbounds"}]`))
	validate := func(_, after []byte) error {
		if !strings.Contains(string(after), "outbounds") {
			return errors.New("outbounds are required")
		}
		return nil
	}
	got, res := e.transform(pipelineDoc, validate)
	if canon(t, []byte(got)) != canon(t, []byte(pipelineDoc)) {
		t.Errorf("a refused patch was applied: %s", got)
	}
	if len(res) != 1 || res[0].Err == nil || !strings.Contains(res[0].Err.Error(), "outbounds are required") {
		t.Errorf("results = %+v", res)
	}
}

func TestTransformSkipsPluginsWithoutTheRightOrNotRunning(t *testing.T) {
	noRight, plug := eventEnv(t, []string{"events:client.created"}, patchReply(`[{"op":"add","path":"/x","value":1}]`))
	if got, res := noRight.transform(pipelineDoc, nil); canon(t, []byte(got)) != canon(t, []byte(pipelineDoc)) || len(res) != 0 || len(plug.seen) != 0 {
		t.Errorf("a plugin without the pipeline permission must not be called: %s %+v %d", got, res, len(plug.seen))
	}

	down, plug2 := pipelineEnv(t, patchReply(`[{"op":"add","path":"/x","value":1}]`))
	down.runner.units["traffic-alerts"].state = StateStopping
	down.reconcile()
	if got, _ := down.transform(pipelineDoc, nil); canon(t, []byte(got)) != canon(t, []byte(pipelineDoc)) || len(plug2.seen) != 0 {
		t.Errorf("a plugin that is not running must be skipped: %s", got)
	}
}

func TestTransformCachesByDocumentAndDoesNotCacheFailures(t *testing.T) {
	var calls atomic.Int32
	fail := atomic.Bool{}
	e, _ := pipelineEnv(t, func(w http.ResponseWriter, r *http.Request, b []byte) {
		calls.Add(1)
		if fail.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		patchReply(`[{"op":"add","path":"/seen","value":true}]`)(w, r, b)
	})

	first, _ := e.transform(pipelineDoc, nil)
	second, _ := e.transform(pipelineDoc, nil)
	if first != second || calls.Load() != 1 {
		t.Fatalf("the same document must not hit the plugin again (calls=%d)", calls.Load())
	}
	e.transform(`{"routing":{"rules":[]},"outbounds":[]}`, nil)
	if calls.Load() != 2 {
		t.Errorf("another document must be sent to the plugin (calls=%d)", calls.Load())
	}

	fail.Store(true)
	other := `{"routing":{"rules":[1]},"outbounds":[]}`
	e.transform(other, nil)
	fail.Store(false)
	got, _ := e.transform(other, nil)
	if !strings.Contains(got, `"seen":true`) {
		t.Errorf("a failure must not be cached: %s", got)
	}
}

func TestTransformNoContentMeansNoChange(t *testing.T) {
	e, _ := pipelineEnv(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) { w.WriteHeader(http.StatusNoContent) })
	got, res := e.transform(pipelineDoc, nil)
	if canon(t, []byte(got)) != canon(t, []byte(pipelineDoc)) || len(res) != 1 || res[0].Err != nil || res[0].Applied != 0 {
		t.Errorf("204: %s %+v", got, res)
	}
}

func TestTransformRunsPluginsInIDOrderAndIsolatesFailures(t *testing.T) {
	sockDir, err := os.MkdirTemp("", "lucxo")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	e := newSupEnv(t, func(m map[string]any) { m["permissions"] = []string{"pipeline:xray.config"} })
	t.Cleanup(e.sup.Close)
	e.runner.socketDir = sockDir

	e.archives["/acme/other/tar.gz/HEAD"] = procArchive(t, func(m map[string]any) {
		m["id"] = "aaa-first"
		m["permissions"] = []string{"pipeline:xray.config"}
	})
	p, err := e.m.Preview(context.Background(), "https://github.com/acme/other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Install(p.Token, p.Manifest.Permissions); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"traffic-alerts", "aaa-first"} {
		if err := e.m.SetEnabled(id, true); err != nil {
			t.Fatal(err)
		}
	}
	e.reconcile()

	listenPlugin(t, sockDir, "aaa-first", func(w http.ResponseWriter, _ *http.Request, body []byte) {
		patchReply(`[{"op":"add","path":"/order","value":["first"]}]`)(w, nil, body)
	})
	listenPlugin(t, sockDir, "traffic-alerts", func(w http.ResponseWriter, _ *http.Request, body []byte) {
		if !strings.Contains(string(body), `"order":["first"]`) {
			http.Error(w, "plugins must run in id order, each seeing the previous result", http.StatusInternalServerError)
			return
		}
		patchReply(`[{"op":"add","path":"/order/-","value":"second"}]`)(w, nil, body)
	})

	got, res := e.transform(pipelineDoc, nil)
	if !strings.Contains(got, `"order":["first","second"]`) {
		t.Errorf("result = %s (results %+v)", got, res)
	}
	if len(res) != 2 || res[0].Plugin != "aaa-first" || res[1].Plugin != "traffic-alerts" {
		t.Errorf("results = %+v", res)
	}
}
