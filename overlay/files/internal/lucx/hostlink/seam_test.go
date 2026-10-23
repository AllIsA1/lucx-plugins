// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostlink

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// seamHost is a plugin host that speaks the seam part of the contract.
type seamHost struct {
	mu       sync.Mutex
	caps     map[string]int
	seams    string // the JSON list sent as "seams"
	preBody  []string
	postBody []string
	preReply string
	slow     time.Duration
	status   int
}

func (h *seamHost) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		defer h.mu.Unlock()
		switch r.URL.Path {
		case "/internal/hello":
			_ = json.NewEncoder(w).Encode(map[string]any{"integrationApi": IntegrationAPI, "host": "t", "capabilities": h.caps})
		case "/internal/subscriptions":
			seams := h.seams
			if seams == "" {
				seams = "[]"
			}
			_, _ = io.WriteString(w, `{"events":[],"pipelines":[],"seams":`+seams+`}`)
		case "/internal/seam/pre":
			b, _ := io.ReadAll(r.Body)
			h.preBody = append(h.preBody, string(b))
			time.Sleep(h.slow)
			if h.status != 0 {
				w.WriteHeader(h.status)
				return
			}
			_, _ = io.WriteString(w, h.preReply)
		case "/internal/seam/post":
			b, _ := io.ReadAll(r.Body)
			h.postBody = append(h.postBody, string(b))
			time.Sleep(h.slow)
			_, _ = io.WriteString(w, `{"response":{"status":200,"body":"cGx1Z2lu"},"changed":true,"failures":0}`)
		default:
			http.NotFound(w, r)
		}
	})
}

func startedSeamLink(t *testing.T, h *seamHost) *Link {
	t.Helper()
	l := newLink(t, fakeHost(t, h.handler()))
	l.Start()
	waitFor(t, func() bool { return l.Status().Connected })
	return l
}

const oneSeam = `[{"engine":"sub","route":"/:subid","methods":["GET"],"pre":true,"post":true},{"engine":"api","route":"/clients/*","pre":false,"post":true}]`

func TestTheSeamTableIsLearnedFromAHostThatHasSeams(t *testing.T) {
	l := startedSeamLink(t, &seamHost{caps: map[string]int{"seams": 1}, seams: oneSeam})
	waitFor(t, func() bool { pre, _ := l.SeamWanted("sub", "GET", "/:subid"); return pre })
	pre, post := l.SeamWanted("sub", "GET", "/:subid")
	if !pre || !post {
		t.Errorf("sub GET /:subid = pre %v post %v, want both", pre, post)
	}
	pre, post = l.SeamWanted("api", "POST", "/clients/add")
	if pre || !post {
		t.Errorf("api POST /clients/add = pre %v post %v, want only post", pre, post)
	}
	for _, c := range []struct{ engine, method, route string }{
		{"sub", "POST", "/:subid"}, {"api", "GET", "/:subid"}, {"sub", "GET", "/json/:subid"}, {"api", "GET", "/inbounds/list"}, {"api", "GET", "/setting/all"},
	} {
		if pre, post := l.SeamWanted(c.engine, c.method, c.route); pre || post {
			t.Errorf("%v takes part although nothing was declared for it", c)
		}
	}
}

func TestAHostWithoutTheCapabilityGetsNoSeamCalls(t *testing.T) {
	l := startedSeamLink(t, &seamHost{caps: nil, seams: oneSeam})
	time.Sleep(100 * time.Millisecond)
	if pre, post := l.SeamWanted("sub", "GET", "/:subid"); pre || post {
		t.Error("seams were used although the host did not announce the capability: an older host's table means something else")
	}
	other := startedSeamLink(t, &seamHost{caps: map[string]int{"seams": 99}, seams: oneSeam})
	time.Sleep(100 * time.Millisecond)
	if pre, _ := other.SeamWanted("sub", "GET", "/:subid"); pre {
		t.Error("a seam contract version this panel does not know was used")
	}
}

func TestTheTableGoesWhenTheHostGoes(t *testing.T) {
	l := newLink(t, socketPath(t, "gone.sock"))
	l.subs.Store(&subscriptions{seams: []seamRow{{Engine: "sub", Route: "/:subid", Pre: true}}})
	l.Refresh(context.Background())
	if pre, _ := l.SeamWanted("sub", "GET", "/:subid"); pre {
		t.Error("the panel keeps calling a host that is not there")
	}
	l.Stop()
	if pre, _ := l.SeamWanted("sub", "GET", "/:subid"); pre {
		t.Error("a stopped link wants seams")
	}
}

func TestSeamPreAndPostCarryTheRequestAndReadTheAnswer(t *testing.T) {
	h := &seamHost{caps: map[string]int{"seams": 1}, seams: oneSeam, preReply: `{"responded":false,"request":{"engine":"sub","method":"GET","route":"/:subid","path":"/sub/abc","query":"x=1"},"response":{},"failures":0}`}
	l := startedSeamLink(t, h)
	req := SeamRequest{Engine: "sub", Method: "GET", Route: "/:subid", Path: "/sub/abc", Query: "a=1", Headers: map[string][]string{"Accept": {"*/*"}}, Body: []byte("b")}
	got, err := l.SeamPre(context.Background(), req)
	if err != nil || got.Responded || got.Request.Query != "x=1" {
		t.Fatalf("SeamPre = %+v, %v", got, err)
	}
	if len(h.preBody) != 1 || !strings.Contains(h.preBody[0], `"route":"/:subid"`) || !strings.Contains(h.preBody[0], `"query":"a=1"`) {
		t.Errorf("the host received %v", h.preBody)
	}
	post, err := l.SeamPost(context.Background(), req, SeamResponse{Status: 200, Body: []byte("panel")})
	if err != nil || !post.Changed || string(post.Response.Body) != "plugin" {
		t.Fatalf("SeamPost = %+v, %v", post, err)
	}
	if len(h.postBody) != 1 || !strings.Contains(h.postBody[0], `"request"`) || !strings.Contains(h.postBody[0], `"response"`) {
		t.Errorf("the host received %v", h.postBody)
	}
}

func TestAFailingOrSlowHostLeavesThePanelAlone(t *testing.T) {
	h := &seamHost{caps: map[string]int{"seams": 1}, seams: oneSeam, status: http.StatusInternalServerError}
	l := startedSeamLink(t, h)
	req := SeamRequest{Engine: "sub", Method: "GET", Route: "/:subid", Path: "/sub/abc"}
	if _, err := l.SeamPre(context.Background(), req); err == nil {
		t.Error("a host error was not reported: the caller must know to run the panel's own code")
	}

	slow := &seamHost{caps: map[string]int{"seams": 1}, seams: oneSeam, slow: 400 * time.Millisecond, preReply: `{}`}
	ls := startedSeamLink(t, slow)
	ls.seamTimeout = 100 * time.Millisecond
	start := time.Now()
	if _, err := ls.SeamPre(context.Background(), req); err == nil || time.Since(start) > 350*time.Millisecond {
		t.Errorf("a slow host held the request %v (err %v)", time.Since(start), err)
	}

	dead := newLink(t, socketPath(t, "dead.sock"))
	if _, err := dead.SeamPre(context.Background(), req); err == nil {
		t.Error("a dead host was not reported")
	}
	if _, err := dead.SeamPost(context.Background(), req, SeamResponse{Status: 200}); err == nil {
		t.Error("a dead host was not reported (post)")
	}
}

func TestARequestBodyOverTheLimitIsNotSentAtAll(t *testing.T) {
	h := &seamHost{caps: map[string]int{"seams": 1}, seams: oneSeam, preReply: `{}`}
	l := startedSeamLink(t, h)
	big := SeamRequest{Engine: "sub", Method: "POST", Route: "/:subid", Body: make([]byte, MaxSeamRequestBody+1)}
	if _, err := l.SeamPre(context.Background(), big); err == nil {
		t.Error("an oversized body went to the host")
	}
	if len(h.preBody) != 0 {
		t.Errorf("the host received %d calls", len(h.preBody))
	}
}

// The panel and the plugin system decide separately whether a seam takes part in a route; the vectors
// in testdata/seam_match.golden are checked on both sides, so they cannot drift apart.
func TestSeamMatchingAgreesWithTheGoldenVectors(t *testing.T) {
	f, err := os.Open("testdata/seam_match.golden")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p := strings.Split(line, "|")
		if len(p) != 6 {
			t.Fatalf("bad vector %q", line)
		}
		var methods []string
		if p[2] != "-" {
			methods = strings.Split(p[2], ",")
		}
		got := seamTakesPart(p[0], p[1], methods, p[4], p[3])
		if got != (p[5] == "1") {
			t.Errorf("%s: takes part = %v", line, got)
		}
		n++
	}
	if n < 30 {
		t.Fatalf("only %d vectors", n)
	}
}
