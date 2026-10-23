// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func req(method, route string) SeamRequest {
	return SeamRequest{Engine: SeamEngineAPI, Method: method, Route: route, Path: "/panel/api" + route, Query: "a=1", Headers: map[string][]string{"Accept": {"*/*"}}, Body: []byte("in")}
}

// scripted answers by plugin id and mode; a missing entry is "no opinion".
type scripted struct {
	mu      sync.Mutex
	replies map[string]seamReply
	errs    map[string]error
	seen    []string
	inputs  map[string]seamEnvelope
}

func (c *scripted) call(_ context.Context, e Entry, mode string, in seamEnvelope) (seamReply, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen = append(c.seen, e.ID+":"+mode)
	if c.inputs == nil {
		c.inputs = map[string]seamEnvelope{}
	}
	c.inputs[e.ID+":"+mode] = in
	if err := c.errs[e.ID+":"+mode]; err != nil {
		return seamReply{}, err
	}
	if r, ok := c.replies[e.ID+":"+mode]; ok {
		return r, nil
	}
	return seamReply{Action: seamActionContinue}, nil
}

func targets(ids ...string) []seamTarget {
	var out []seamTarget
	for _, id := range ids {
		out = append(out, seamTarget{entry: Entry{ID: id}})
	}
	return out
}

func respond(status int, body string) seamReply {
	return seamReply{Action: seamActionRespond, Response: &SeamResponseDelta{Body: bytesPtr(body), Status: ptr(status), Headers: map[string][]string{"Content-Type": {"text/plain"}}}}
}

func TestBeforeChainPassesTheRequestOnAndTheFirstAnswerWins(t *testing.T) {
	c := &scripted{replies: map[string]seamReply{
		"a:before": {Action: seamActionContinue, Request: &SeamRequestDelta{Query: ptr("b=2"), Body: bytesPtr("a-body")}},
	}}
	got := runPre(context.Background(), req("GET", "/clients/list"), targets("a", "b"), nil, c.call)
	if got.Responded || got.Request.Query != "b=2" || string(got.Request.Body) != "a-body" {
		t.Fatalf("result = %+v", got)
	}
	if in := c.inputs["b:before"]; in.Request.Query != "b=2" || string(in.Request.Body) != "a-body" {
		t.Errorf("the second plugin saw %+v, want what the first one made of the request", in.Request)
	}

	c = &scripted{replies: map[string]seamReply{"a:before": respond(403, "no")}}
	got = runPre(context.Background(), req("GET", "/clients/list"), targets("a", "b"), nil, c.call)
	if !got.Responded || got.Response.Status != 403 || string(got.Response.Body) != "no" {
		t.Fatalf("result = %+v", got)
	}
	if strings.Join(c.seen, ",") != "a:before" {
		t.Errorf("calls = %v: after an answer nobody else should be asked", c.seen)
	}
}

func TestReplaceRunsAfterBeforeAndFallsBackToThePanel(t *testing.T) {
	c := &scripted{replies: map[string]seamReply{"r:replace": respond(200, "mine")}}
	got := runPre(context.Background(), req("GET", "/clients/list"), targets("a"), &targets("r")[0], c.call)
	if !got.Responded || string(got.Response.Body) != "mine" || strings.Join(c.seen, ",") != "a:before,r:replace" {
		t.Errorf("result = %+v, calls %v", got, c.seen)
	}

	c = &scripted{replies: map[string]seamReply{"r:replace": {Action: seamActionFallback}}}
	got = runPre(context.Background(), req("GET", "/clients/list"), nil, &targets("r")[0], c.call)
	if got.Responded {
		t.Error("a replace plugin that falls back must leave the request to the panel")
	}
	c = &scripted{errs: map[string]error{"r:replace": errors.New("boom")}}
	got = runPre(context.Background(), req("GET", "/clients/list"), nil, &targets("r")[0], c.call)
	if got.Responded {
		t.Error("a failing replace plugin must leave the request to the panel")
	}
}

func TestAFailingOrLyingPluginIsSkippedNotObeyed(t *testing.T) {
	bad := map[string]seamReply{
		"unknown action":                {Action: "explode"},
		"status out of range":           respond(99, "x"),
		"status 1000":                   respond(1000, "x"),
		"cookie header":                 {Action: seamActionRespond, Response: &SeamResponseDelta{Status: ptr(200), Headers: map[string][]string{"Set-Cookie": {"3x-ui=evil"}}}},
		"csp header":                    {Action: seamActionRespond, Response: &SeamResponseDelta{Status: ptr(200), Headers: map[string][]string{"Content-Security-Policy": {"default-src *"}}}},
		"huge body":                     {Action: seamActionRespond, Response: &SeamResponseDelta{Status: ptr(200), Body: bytesPtr(strings.Repeat("x", maxSeamBody+1))}},
		"changes headers the panel set": {Action: seamActionContinue, Request: &SeamRequestDelta{Headers: map[string][]string{"Authorization": {"Bearer x"}}}},
		"respond without a response":    {Action: seamActionRespond},
	}
	for name, reply := range bad {
		t.Run(name, func(t *testing.T) {
			c := &scripted{replies: map[string]seamReply{"a:before": reply}}
			orig := req("GET", "/clients/list")
			got := runPre(context.Background(), orig, targets("a", "b"), nil, c.call)
			if got.Responded || got.Request.Query != orig.Query || string(got.Request.Body) != "in" {
				t.Errorf("a refused answer changed the outcome: %+v", got)
			}
			if got.Failures != 1 {
				t.Errorf("failures = %d, want the bad answer counted once", got.Failures)
			}
			if !strings.Contains(strings.Join(c.seen, ","), "b:before") {
				t.Errorf("calls = %v: the plugins after a bad one must still be asked", c.seen)
			}
		})
	}
	c := &scripted{errs: map[string]error{"a:before": errors.New("boom")}}
	got := runPre(context.Background(), req("GET", "/x"), targets("a"), nil, c.call)
	if got.Responded || got.Failures != 1 {
		t.Errorf("a failing call = %+v", got)
	}
}

func TestAfterChainModifiesTheResponseStepByStep(t *testing.T) {
	base := SeamResponse{Status: 200, Headers: map[string][]string{"Content-Type": {"text/plain"}, "X-Keep": {"1"}}, Body: []byte("panel")}
	c := &scripted{replies: map[string]seamReply{
		"a:after": {Action: seamActionModify, Response: &SeamResponseDelta{Body: bytesPtr("a"), Headers: map[string][]string{"X-A": {"1"}}}},
		"b:after": {Action: seamActionModify, Response: &SeamResponseDelta{Status: ptr(201), Headers: map[string][]string{"X-Keep": nil}}},
	}}
	got := runPost(context.Background(), req("GET", "/x"), base, targets("a", "b"), c.call)
	if got.Response.Status != 201 || string(got.Response.Body) != "a" || got.Response.Headers["X-A"][0] != "1" || got.Response.Headers["Content-Type"][0] != "text/plain" {
		t.Errorf("response = %+v", got.Response)
	}
	if _, ok := got.Response.Headers["X-Keep"]; ok {
		t.Error("an emptied header must be removed")
	}
	if !got.Changed {
		t.Error("Changed must say the response differs from the panel's")
	}
	if in := c.inputs["b:after"]; string(in.Response.Body) != "a" {
		t.Errorf("the second plugin saw %q, want the first one's result", in.Response.Body)
	}

	// Nobody has an opinion: the panel's own answer, untouched.
	got = runPost(context.Background(), req("GET", "/x"), base, targets("a"), (&scripted{}).call)
	if got.Changed || string(got.Response.Body) != "panel" {
		t.Errorf("no opinion = %+v", got)
	}

	// A refused modification is skipped and does not stop the chain.
	c = &scripted{replies: map[string]seamReply{
		"a:after": {Action: seamActionModify, Response: &SeamResponseDelta{Headers: map[string][]string{"Set-Cookie": {"x=y"}}}},
		"b:after": {Action: seamActionModify, Response: &SeamResponseDelta{Body: bytesPtr("b")}},
	}}
	got = runPost(context.Background(), req("GET", "/x"), base, targets("a", "b"), c.call)
	if string(got.Response.Body) != "b" || got.Failures != 1 {
		t.Errorf("after a refused modification: %+v", got)
	}
	if _, ok := got.Response.Headers["Set-Cookie"]; ok {
		t.Error("a forbidden header got through")
	}
}

func TestOnlyAllowedRequestPartsMayBeChanged(t *testing.T) {
	c := &scripted{replies: map[string]seamReply{"a:before": {Action: seamActionContinue, Request: &SeamRequestDelta{
		Headers: map[string][]string{"Accept-Language": {"ru"}, "X-Plugin": {"1"}},
	}}}}
	got := runPre(context.Background(), req("GET", "/x"), targets("a"), nil, c.call)
	if got.Request.Headers["Accept-Language"][0] != "ru" || got.Request.Headers["X-Plugin"][0] != "1" || got.Request.Headers["Accept"][0] != "*/*" {
		t.Errorf("request headers = %v", got.Request.Headers)
	}
	if got.Request.Path != "/panel/api/x" || got.Request.Route != "/x" || got.Request.Method != "GET" {
		t.Errorf("the route and path are the panel's and must not move: %+v", got.Request)
	}
}

func TestSeamsAreMatchedToTheRequestAndOrderedByInstall(t *testing.T) {
	w := newSupEnv(t, func(m map[string]any) {
		m["permissions"] = []string{"seam:api:clients"}
		m["seams"] = []map[string]any{
			{"engine": "api", "mode": "after", "methods": []string{"GET"}, "route": "/clients/list"},
			{"engine": "api", "mode": "before", "route": "/clients/*"},
		}
	})
	w.enable(t)
	w.reconcile()
	if got := w.sup.seamTargets(SeamEngineAPI, "GET", "/clients/list", SeamAfter); len(got) != 1 {
		t.Errorf("after targets for GET /clients/list = %d, want 1", len(got))
	}
	for _, c := range []struct {
		method, route, mode string
		want                int
	}{
		{"POST", "/clients/list", SeamAfter, 0},
		{"GET", "/clients/add", SeamAfter, 0},
		{"POST", "/clients/add", SeamBefore, 1},
		{"GET", "/inbounds/list", SeamBefore, 0},
		{"GET", "/setting/all", SeamBefore, 0},
		{"GET", "/clients/list", SeamReplace, 0},
		{"GET", "/clients/list", "before", 1},
	} {
		if got := w.sup.seamTargets(SeamEngineAPI, c.method, c.route, c.mode); len(got) != c.want {
			t.Errorf("%s %s %s: %d targets, want %d", c.method, c.route, c.mode, len(got), c.want)
		}
	}
	if got := w.sup.seamTargets(SeamEngineSub, "GET", "/:subid", SeamAfter); len(got) != 0 {
		t.Error("an api seam matched the subscription engine")
	}
}

func TestOnlyAnEnabledRunningPluginHoldingThePermissionTakesPart(t *testing.T) {
	seams := []map[string]any{{"engine": "api", "mode": "after", "route": "/clients/list"}}
	// Declared but never granted: the manifest is refused at install, so build the entry by hand.
	w := newSupEnv(t, func(m map[string]any) { m["permissions"] = []string{"seam:api:clients"}; m["seams"] = seams })
	if got := w.sup.seamTargets(SeamEngineAPI, "GET", "/clients/list", SeamAfter); len(got) != 0 {
		t.Error("a disabled plugin takes part")
	}
	w.enable(t)
	w.reconcile()
	if got := w.sup.seamTargets(SeamEngineAPI, "GET", "/clients/list", SeamAfter); len(got) != 1 {
		t.Fatal("an enabled running plugin does not take part")
	}
	w.runner.units["traffic-alerts"].state = StateStopping
	w.reconcile()
	if got := w.sup.seamTargets(SeamEngineAPI, "GET", "/clients/list", SeamAfter); len(got) != 0 {
		t.Error("a plugin that is not running takes part: the panel would wait for nothing")
	}

	e, _ := w.m.reg.Get("traffic-alerts")
	e.Permissions = nil
	if got := effectiveSeams(e); len(got) != 0 {
		t.Errorf("seams without the permission are effective: %+v", got)
	}
}

func TestTheSocketCallCarriesTheEnvelopeAndReadsTheAnswer(t *testing.T) {
	e, plug := seamEnvWith(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"action":"respond","response":{"status":202,"headers":{"Content-Type":["text/plain"]},"body":"aGVsbG8="}}`))
	}, nil)
	got := e.sup.SeamPre(context.Background(), req("GET", "/clients/list"))
	if !got.Responded || got.Response.Status != 202 || string(got.Response.Body) != "hello" {
		t.Fatalf("result = %+v", got)
	}
	reqs := plug.requests("/_lucx/seam/before")
	if len(reqs) != 1 || reqs[0].Method != http.MethodPost {
		t.Fatalf("plugin requests: %+v", plug.seen)
	}
	var in struct {
		Seam    struct{ Engine, Mode, Route string }
		Request struct {
			Method, Route, Path, Query string
			Body                       []byte
			Headers                    map[string][]string
		}
	}
	if err := json.Unmarshal([]byte(reqs[0].Body), &in); err != nil {
		t.Fatal(err)
	}
	if in.Seam.Engine != "api" || in.Seam.Mode != "before" || in.Seam.Route != "/clients/list" || in.Request.Method != "GET" || in.Request.Route != "/clients/list" ||
		in.Request.Query != "a=1" || string(in.Request.Body) != "in" || in.Request.Headers["Accept"][0] != "*/*" {
		t.Errorf("the plugin received %+v", in)
	}
}

func seamEnvWith(t *testing.T, handler func(http.ResponseWriter, *http.Request, []byte), mutate func(m map[string]any), opts ...func(*SupervisorOptions)) (*supEnv, *pluginSocket) {
	t.Helper()
	return eventEnvSeams(t, []string{"seam:api:clients", "seam:sub"}, []map[string]any{
		{"engine": "api", "mode": "before", "route": "/clients/list"},
		{"engine": "api", "mode": "after", "route": "/clients/list"},
		{"engine": "sub", "mode": "replace", "route": "/:subid"},
	}, handler, opts...)
}

func TestACrashingPluginIsSkippedQuicklyAndThenLeftAloneForAWhile(t *testing.T) {
	calls := 0
	var mu sync.Mutex
	e, plug := seamEnvWith(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		mu.Lock()
		calls++
		mu.Unlock()
		http.Error(w, "boom", http.StatusInternalServerError)
	}, nil, func(o *SupervisorOptions) { o.SeamBreakerThreshold = 3; o.SeamBreakerCooldown = 200 * time.Millisecond })
	_ = plug
	for i := 0; i < 3; i++ {
		if got := e.sup.SeamPre(context.Background(), req("GET", "/clients/list")); got.Responded {
			t.Fatal("a failing plugin answered")
		}
	}
	mu.Lock()
	before := calls
	mu.Unlock()
	if before != 3 {
		t.Fatalf("calls = %d, want 3", before)
	}
	for i := 0; i < 5; i++ {
		e.sup.SeamPre(context.Background(), req("GET", "/clients/list"))
	}
	mu.Lock()
	after := calls
	mu.Unlock()
	if after != before {
		t.Errorf("the plugin was called %d more times while the breaker was open", after-before)
	}
	rt, _ := e.sup.Runtime("traffic-alerts")
	if rt.SeamErrors != 3 || rt.SeamSkipped < 5 {
		t.Errorf("runtime = errors %d skipped %d, want 3 errors and the later calls counted as skipped", rt.SeamErrors, rt.SeamSkipped)
	}
	if table := e.sup.SeamTable(); len(table) != 0 {
		t.Errorf("table = %+v: a plugin whose breaker is open must not be advertised to the panel", table)
	}

	time.Sleep(250 * time.Millisecond)
	e.sup.SeamPre(context.Background(), req("GET", "/clients/list"))
	mu.Lock()
	again := calls
	mu.Unlock()
	if again != after+1 {
		t.Errorf("after the cool-down the plugin was called %d times, want exactly one trial", again-after)
	}
}

func TestASlowPluginIsCutOff(t *testing.T) {
	e, _ := seamEnvWith(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		time.Sleep(400 * time.Millisecond)
		_, _ = w.Write([]byte(`{"action":"respond","response":{"status":200}}`))
	}, nil, func(o *SupervisorOptions) { o.SeamTimeout = 100 * time.Millisecond })
	start := time.Now()
	got := e.sup.SeamPre(context.Background(), req("GET", "/clients/list"))
	if got.Responded || time.Since(start) > 350*time.Millisecond {
		t.Errorf("a slow plugin held the request for %v and answered=%v", time.Since(start), got.Responded)
	}
}

func TestTheTableAdvertisesWhatIsReadyAndNothingElse(t *testing.T) {
	e, _ := seamEnvWith(t, nil, nil)
	table := e.sup.SeamTable()
	want := map[string]bool{"api|/clients/list|pre": false, "api|/clients/list|post": false, "sub|/:subid|pre": false}
	for _, row := range table {
		if row.Pre {
			want[row.Engine+"|"+row.Route+"|pre"] = true
		}
		if row.Post {
			want[row.Engine+"|"+row.Route+"|post"] = true
		}
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("the table lacks %s: %+v", k, table)
		}
	}
	e.runner.units["traffic-alerts"].state = StateStopping
	e.reconcile()
	if table := e.sup.SeamTable(); len(table) != 0 {
		t.Errorf("a stopped plugin is advertised: %+v", table)
	}
}

func ptr[T any](v T) *T { return &v }

func bytesPtr(s string) *[]byte { b := []byte(s); return &b }

// eventEnvSeams is eventEnv for a plugin whose manifest declares seams.
func eventEnvSeams(t *testing.T, perms []string, seams []map[string]any, handler func(w http.ResponseWriter, r *http.Request, body []byte), opts ...func(*SupervisorOptions)) (*supEnv, *pluginSocket) {
	t.Helper()
	sockDir, err := os.MkdirTemp("", "lucxs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	e := newSupEnv(t, func(m map[string]any) { m["permissions"] = perms; m["seams"] = seams }, opts...)
	t.Cleanup(e.sup.Close)
	e.runner.socketDir = sockDir
	e.enable(t)
	e.reconcile()
	return e, listenPlugin(t, sockDir, "traffic-alerts", handler)
}

func TestSeamsFollowThePluginThroughInstallUpdateAndRollback(t *testing.T) {
	seamsV1 := []map[string]any{{"engine": "api", "mode": "after", "route": "/clients/list"}}
	seamsV2 := []map[string]any{{"engine": "sub", "mode": "after", "route": "/:subid"}}
	env := newEnv(t, map[string][]byte{updPath: archiveAt(t, testCommit, "1.2.0", func(m map[string]any) {
		m["permissions"] = []string{"seam:api:clients"}
		m["seams"] = seamsV1
	})})
	env.m.SetProcessController(&fakeProc{})
	ctx := context.Background()
	p, err := env.m.Preview(ctx, updURL)
	if err != nil {
		t.Fatal(err)
	}
	e, err := env.m.Install(p.Token, []string{"seam:api:clients"})
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Seams) != 1 || e.Seams[0].Route != "/clients/list" || e.Seams[0].Engine != "api" {
		t.Fatalf("installed seams = %+v", e.Seams)
	}

	env.publish(archiveAt(t, commitV2, "1.3.0", func(m map[string]any) {
		m["permissions"] = []string{"seam:sub"}
		m["seams"] = seamsV2
	}))
	up, err := env.m.PreviewUpdate(ctx, "traffic-alerts", "")
	if err != nil {
		t.Fatal(err)
	}
	e, err = env.m.ApplyUpdate(ctx, "traffic-alerts", up.Token, []string{"seam:sub"})
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Seams) != 1 || e.Seams[0].Engine != "sub" || e.Previous == nil || len(e.Previous.Seams) != 1 || e.Previous.Seams[0].Route != "/clients/list" {
		t.Fatalf("after the update: seams %+v, previous %+v", e.Seams, e.Previous)
	}
	e, err = env.m.Rollback(ctx, "traffic-alerts")
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Seams) != 1 || e.Seams[0].Route != "/clients/list" {
		t.Errorf("after the rollback: seams %+v", e.Seams)
	}
	again, err := NewManager(env.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := again.Get("traffic-alerts"); len(got.Seams) != 1 || got.Seams[0].Route != "/clients/list" {
		t.Errorf("after a restart: %+v", got.Seams)
	}
	// The registry hands out copies: changing one must not change the stored entry.
	got, _ := again.Get("traffic-alerts")
	got.Seams[0].Route = "/tampered"
	if again2, _ := again.Get("traffic-alerts"); again2.Seams[0].Route == "/tampered" {
		t.Error("the registry shares its seams with callers")
	}
}

func TestSwitchingAPluginOffTakesEffectBeforeItsProcessStops(t *testing.T) {
	e, _ := seamEnvWith(t, nil, nil)
	if got := e.sup.seamTargets(SeamEngineAPI, "GET", "/clients/list", SeamAfter); len(got) != 1 {
		t.Fatal("setup: the plugin does not take part")
	}
	if err := e.m.SetEnabled("traffic-alerts", false); err != nil {
		t.Fatal(err)
	}
	// No reconcile yet: the process is still running, but the panel must already stop asking it.
	if got := e.sup.seamTargets(SeamEngineAPI, "GET", "/clients/list", SeamAfter); len(got) != 0 {
		t.Error("a plugin that was just switched off is still asked")
	}
	if table := e.sup.SeamTable(); len(table) != 0 {
		t.Errorf("a plugin that was just switched off is still advertised: %+v", table)
	}
}

func TestThePluginNeverSeesTheSessionOrTheTokens(t *testing.T) {
	e, plug := seamEnvWith(t, nil, nil)
	r := req("GET", "/clients/list")
	r.Headers = map[string][]string{
		"Cookie": {"3x-ui=secret"}, "Authorization": {"Bearer secret"}, "X-Csrf-Token": {"secret"}, "X-LucX-User": {"forged"},
		"X-Forwarded-For": {"1.2.3.4"}, "Accept": {"*/*"},
	}
	e.sup.SeamPre(context.Background(), r)
	reqs := plug.requests("/_lucx/seam/before")
	if len(reqs) != 1 {
		t.Fatalf("plugin requests: %d", len(reqs))
	}
	for _, secret := range []string{"secret", "3x-ui=", "forged", "1.2.3.4"} {
		if strings.Contains(reqs[0].Body, secret) {
			t.Errorf("the plugin received %q: %s", secret, reqs[0].Body)
		}
	}
	if !strings.Contains(reqs[0].Body, "*/*") {
		t.Error("ordinary headers must still reach the plugin")
	}
}

func TestARequestBodyBeyondTheLimitCannotBeSetByAPlugin(t *testing.T) {
	big := strings.Repeat("x", maxSeamRequest+1)
	c := &scripted{replies: map[string]seamReply{"a:before": {Action: seamActionContinue, Request: &SeamRequestDelta{Body: bytesPtr(big)}}}}
	got := runPre(context.Background(), req("POST", "/clients/add"), targets("a"), nil, c.call)
	if string(got.Request.Body) != "in" || got.Failures != 1 {
		t.Errorf("an oversized request body was accepted: %d bytes, failures %d", len(got.Request.Body), got.Failures)
	}
}

func TestTheTableSaysWhichSidesAnEntryHas(t *testing.T) {
	e, _ := seamEnvWith(t, nil, nil)
	for _, row := range e.sup.SeamTable() {
		switch row.Engine + row.Route {
		case "api/clients/list":
			if !row.Pre || !row.Post {
				t.Errorf("a route with a before and an after seam: %+v", row)
			}
		case "sub/:subid":
			if !row.Pre || row.Post {
				t.Errorf("a replace-only route needs the call before the panel's handler and nothing after: %+v", row)
			}
		default:
			t.Errorf("unexpected row %+v", row)
		}
	}
}

func TestAfterTheCooldownOneFailureOpensTheBreakerAgain(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	e, _ := seamEnvWith(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		mu.Lock()
		calls++
		mu.Unlock()
		http.Error(w, "boom", http.StatusInternalServerError)
	}, nil, func(o *SupervisorOptions) { o.SeamBreakerThreshold = 3; o.SeamBreakerCooldown = 150 * time.Millisecond })
	for i := 0; i < 3; i++ {
		e.sup.SeamPre(context.Background(), req("GET", "/clients/list"))
	}
	time.Sleep(200 * time.Millisecond)
	e.sup.SeamPre(context.Background(), req("GET", "/clients/list")) // the trial
	e.sup.SeamPre(context.Background(), req("GET", "/clients/list")) // must be skipped: the trial failed
	e.sup.SeamPre(context.Background(), req("GET", "/clients/list"))
	mu.Lock()
	defer mu.Unlock()
	if calls != 4 {
		t.Errorf("calls = %d, want 3 failures, one trial after the cool-down and then silence", calls)
	}
}
