// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AllIsA1/lucx-plugins/internal/plugins"
)

type seamFixture struct {
	*internalFixture
	pre  []plugins.SeamRequest
	post []plugins.SeamRequest
}

func newSeamFixture(t *testing.T) *seamFixture {
	t.Helper()
	f := &seamFixture{}
	hooks := Hooks{
		Version:       "test",
		Dispatch:      func(plugins.Event) {},
		Transform:     func(_ context.Context, _ string, doc []byte, _ func(before, after []byte) error) []byte { return doc },
		Subscriptions: func() ([]string, []string) { return nil, nil },
		SeamTable: func() []plugins.SeamTableRow {
			return []plugins.SeamTableRow{{Engine: "sub", Route: "/:subid", Methods: []string{"GET"}, Pre: true, Post: true}}
		},
		SeamPre: func(_ context.Context, req plugins.SeamRequest) plugins.SeamPreResult {
			f.pre = append(f.pre, req)
			if req.Route == "/blocked" {
				return plugins.SeamPreResult{Responded: true, Response: plugins.SeamResponse{Status: 403, Body: []byte("no")}}
			}
			req.Query = "changed=1"
			return plugins.SeamPreResult{Request: req}
		},
		SeamPost: func(_ context.Context, req plugins.SeamRequest, resp plugins.SeamResponse) plugins.SeamPostResult {
			f.post = append(f.post, req)
			resp.Body = append(resp.Body, []byte("+plugin")...)
			return plugins.SeamPostResult{Response: resp, Changed: true}
		},
	}
	srv := NewServer(&PluginService{Provider: func() (*plugins.Manager, error) { return nil, plugins.ErrDisabled }})
	srv.SetHooks(hooks)
	f.internalFixture = &internalFixture{srv: httptest.NewServer(srv.Handler())}
	t.Cleanup(f.srv.Close)
	return f
}

func TestHelloSaysThatThisHostHasSeamsWithoutChangingTheContractVersion(t *testing.T) {
	f := newSeamFixture(t)
	_, body := f.do(http.MethodGet, "/internal/hello", "")
	var hello struct {
		IntegrationAPI int            `json:"integrationApi"`
		Capabilities   map[string]int `json:"capabilities"`
	}
	if err := json.Unmarshal([]byte(body), &hello); err != nil {
		t.Fatal(err)
	}
	if hello.IntegrationAPI != 1 {
		t.Errorf("integrationApi = %d: a panel built for 1 must keep working with this host", hello.IntegrationAPI)
	}
	if hello.Capabilities["seams"] != 1 {
		t.Errorf("capabilities = %v, want seams 1", hello.Capabilities)
	}
}

func TestSubscriptionsCarryTheSeamTable(t *testing.T) {
	f := newSeamFixture(t)
	_, body := f.do(http.MethodGet, "/internal/subscriptions", "")
	var got struct {
		Events, Pipelines []string
		Seams             []plugins.SeamTableRow
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Seams) != 1 || got.Seams[0].Route != "/:subid" || !got.Seams[0].Pre || !got.Seams[0].Post {
		t.Errorf("seams = %+v", got.Seams)
	}
	// A host with nothing to say still sends a list, never null.
	plain := newInternalFixture(t)
	_, body = plain.do(http.MethodGet, "/internal/subscriptions", "")
	if !strings.Contains(body, `"seams":[]`) {
		t.Errorf("without seams the field must be an empty list: %s", body)
	}
}

func TestSeamPreAndPostRunTheHooks(t *testing.T) {
	f := newSeamFixture(t)
	code, body := f.do(http.MethodPost, "/internal/seam/pre", `{"engine":"sub","method":"GET","route":"/:subid","path":"/sub/abc","query":"a=1"}`)
	var pre plugins.SeamPreResult
	if code != 200 || json.Unmarshal([]byte(body), &pre) != nil || pre.Responded || pre.Request.Query != "changed=1" {
		t.Fatalf("pre = %d %s", code, body)
	}
	code, body = f.do(http.MethodPost, "/internal/seam/pre", `{"engine":"sub","method":"GET","route":"/blocked","path":"/sub/abc"}`)
	if code != 200 || json.Unmarshal([]byte(body), &pre) != nil || !pre.Responded || pre.Response.Status != 403 || string(pre.Response.Body) != "no" {
		t.Fatalf("pre answering = %d %s", code, body)
	}
	code, body = f.do(http.MethodPost, "/internal/seam/post", `{"request":{"engine":"sub","method":"GET","route":"/:subid","path":"/sub/abc"},"response":{"status":200,"body":"cGFuZWw="}}`)
	var post plugins.SeamPostResult
	if code != 200 || json.Unmarshal([]byte(body), &post) != nil || !post.Changed || string(post.Response.Body) != "panel+plugin" {
		t.Fatalf("post = %d %s", code, body)
	}
	if len(f.pre) != 2 || len(f.post) != 1 || f.post[0].Route != "/:subid" {
		t.Errorf("hooks saw pre %d post %d", len(f.pre), len(f.post))
	}
}

func TestSeamRoutesRefuseWhatIsNotARequest(t *testing.T) {
	f := newSeamFixture(t)
	for name, c := range map[string]struct{ path, body string }{
		"pre with bad json":       {"/internal/seam/pre", "not json"},
		"pre without engine":      {"/internal/seam/pre", `{"method":"GET","route":"/x"}`},
		"pre with a wrong engine": {"/internal/seam/pre", `{"engine":"ws","method":"GET","route":"/x"}`},
		"pre without a route":     {"/internal/seam/pre", `{"engine":"api","method":"GET"}`},
		"post with bad json":      {"/internal/seam/post", "{"},
		"post without a response": {"/internal/seam/post", `{"request":{"engine":"api","method":"GET","route":"/x"}}`},
		"an unknown phase":        {"/internal/seam/sideways", `{}`},
	} {
		code, _ := f.do(http.MethodPost, c.path, c.body)
		if code != http.StatusBadRequest && code != http.StatusNotFound {
			t.Errorf("%s = %d, want 400 or 404", name, code)
		}
	}
	code, _ := f.do(http.MethodPost, "/internal/seam/pre", `{"engine":"api","method":"GET","route":"/x","body":"`+strings.Repeat("A", 3<<20)+`"}`)
	if code != http.StatusRequestEntityTooLarge {
		t.Errorf("a request body beyond the limit = %d, want 413", code)
	}
	if len(f.pre)+len(f.post) != 0 {
		t.Errorf("hooks ran for refused requests: %d %d", len(f.pre), len(f.post))
	}
}

func TestWithoutASupervisorSeamsAreInvisible(t *testing.T) {
	svc := &PluginService{Provider: func() (*plugins.Manager, error) { return nil, plugins.ErrDisabled }}
	srv := NewServer(svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	post := func(path, body string) string {
		resp, err := http.Post(ts.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			sb.Write(buf[:n])
			if err != nil {
				break
			}
		}
		return sb.String()
	}
	if got := post("/internal/seam/pre", `{"engine":"api","method":"GET","route":"/x","query":"q=1"}`); !strings.Contains(got, `"responded":false`) || !strings.Contains(got, `"query":"q=1"`) {
		t.Errorf("pre without a supervisor = %s, want the request back untouched", got)
	}
	if got := post("/internal/seam/post", `{"request":{"engine":"api","method":"GET","route":"/x"},"response":{"status":201}}`); !strings.Contains(got, `"changed":false`) || !strings.Contains(got, `"status":201`) {
		t.Errorf("post without a supervisor = %s, want the response back untouched", got)
	}
}
