// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostlink

import (
	"bytes"
	stdgzip "compress/gzip"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-contrib/gzip"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/session"
)

const (
	subSeams   = `[{"engine":"sub","route":"/links/:subid","methods":["GET","HEAD"],"pre":true,"post":true}]`
	subAnyBoth = `[{"engine":"sub","route":"/links/:subid","pre":true,"post":true}]`
	subPreOnly = `[{"engine":"sub","route":"/links/:subid","pre":true,"post":false}]`
	subPostOnl = `[{"engine":"sub","route":"/links/:subid","pre":false,"post":true}]`
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

type subSeen struct {
	length int64
	calls  atomic.Int32
	query  string
	body   string
	header http.Header
}

// subEngine is a subscription server in miniature: gzip, the seam middleware, one handler that echoes
// what it was given into its answer.
func subEngine(t *testing.T, h *seamHost, handler gin.HandlerFunc) (*httptest.Server, *subSeen) {
	t.Helper()
	l := startedSeamLink(t, h)
	prev := Shared()
	SetShared(l)
	t.Cleanup(func() { SetShared(prev) })
	waitFor(t, func() bool { pre, post := l.SeamWanted("sub", "GET", "/links/:subid"); return pre || post })
	s := &subSeen{}
	e := gin.New()
	e.Use(gzip.Gzip(gzip.DefaultCompression))
	e.Use(SeamMiddleware(EngineSub, func(c *gin.Context) string {
		if c.FullPath() == "/sub/:subid" {
			return "/links/:subid"
		}
		return ""
	}))
	if handler == nil {
		handler = func(c *gin.Context) {
			raw, _ := io.ReadAll(c.Request.Body)
			s.calls.Add(1)
			s.length = c.Request.ContentLength
			s.query, s.body, s.header = c.Request.URL.RawQuery, string(raw), c.Request.Header.Clone()
			c.Header("X-Panel", "1")
			c.Header("Content-Security-Policy", "default-src 'self'")
			c.Data(200, "text/plain", []byte("panel:"+c.Param("subid")))
		}
	}
	e.GET("/sub/:subid", handler)
	e.POST("/sub/:subid", handler)
	e.HEAD("/sub/:subid", handler)
	e.GET("/other", func(c *gin.Context) { c.String(200, "other") })
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	return srv, s
}

func rawGet(t *testing.T, url string, mutate ...func(*http.Request)) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	for _, m := range mutate {
		m(req)
	}
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.Header.Get("Content-Encoding") == "gzip" {
		zr, err := stdgzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("labelled gzip but not gzip: %q", raw)
		}
		raw, _ = io.ReadAll(zr)
	}
	return resp, string(raw)
}

func TestWithoutSeamsTheMiddlewareChangesNothing(t *testing.T) {
	h := &seamHost{caps: map[string]int{"seams": 1}, seams: `[]`}
	l := startedSeamLink(t, h)
	prev := Shared()
	SetShared(l)
	defer SetShared(prev)
	e := gin.New()
	e.Use(SeamMiddleware(EngineSub, func(c *gin.Context) string { return "/links/:subid" }))
	e.GET("/sub/:subid", func(c *gin.Context) { c.String(200, "panel") })
	srv := httptest.NewServer(e)
	defer srv.Close()
	resp, body := rawGet(t, srv.URL+"/sub/abc")
	if resp.StatusCode != 200 || body != "panel" || len(h.preBody)+len(h.postBody) != 0 {
		t.Errorf("= %d %q, host calls %d: with no seams the host must not even be asked", resp.StatusCode, body, len(h.preBody)+len(h.postBody))
	}
}

func TestBeforeCanAnswerInsteadOfThePanel(t *testing.T) {
	h := &seamHost{caps: map[string]int{"seams": 1}, seams: subPreOnly,
		preReply: `{"responded":true,"request":{},"response":{"status":418,"headers":{"Content-Type":["text/plain"],"X-Plugin":["yes"],"Set-Cookie":["x=evil"]},"body":"` + b64("teapot") + `"},"failures":0}`}
	srv, s := subEngine(t, h, nil)
	resp, body := rawGet(t, srv.URL+"/sub/abc")
	if resp.StatusCode != 418 || body != "teapot" || resp.Header.Get("X-Plugin") != "yes" {
		t.Errorf("= %d %q %v", resp.StatusCode, body, resp.Header)
	}
	if resp.Header.Get("Set-Cookie") != "" {
		t.Error("a cookie from a plugin reached the client even though the host should have refused it")
	}
	if s.calls.Load() != 0 {
		t.Error("the panel's handler ran although a plugin answered")
	}
}

func TestBeforeCanChangeWhatTheHandlerSees(t *testing.T) {
	h := &seamHost{caps: map[string]int{"seams": 1}, seams: subPreOnly, preFn: func(in string) string {
		var req SeamRequest
		_ = json.Unmarshal([]byte(in), &req)
		req.Query = "format=json"
		req.Body = []byte("newer-body")
		req.Headers["Accept-Language"] = []string{"ru"}
		delete(req.Headers, "X-Drop")
		out, _ := json.Marshal(map[string]any{"responded": false, "request": req, "response": map[string]any{}, "failures": 0})
		return string(out)
	}}
	srv, s := subEngine(t, h, nil)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/sub/abc?a=1", strings.NewReader("old-body"))
	req.Header.Set("X-Drop", "1")
	req.Header.Set("Accept-Language", "en")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if s.length != int64(len("newer-body")) {
		t.Errorf("the handler was told the body is %d bytes", s.length)
	}
	if s.query != "format=json" || s.body != "newer-body" || s.header.Get("Accept-Language") != "ru" || s.header.Get("X-Drop") != "" {
		t.Errorf("handler saw query %q body %q headers %v", s.query, s.body, s.header)
	}
	if len(h.preBody) != 1 || !strings.Contains(h.preBody[0], `"query":"a=1"`) || !strings.Contains(h.preBody[0], b64("old-body")) {
		t.Errorf("the host received %v", h.preBody)
	}
}

func TestAfterChangesTheAnswerAndKeepsTheTransportCorrect(t *testing.T) {
	h := &seamHost{caps: map[string]int{"seams": 1}, seams: subPostOnl, postFn: func(in string) string {
		var got struct{ Response SeamResponse }
		_ = json.Unmarshal([]byte(in), &got)
		got.Response.Body = append(got.Response.Body, []byte("+plugin")...)
		got.Response.Status = 201
		got.Response.Headers["X-Plugin"] = []string{"1"}
		delete(got.Response.Headers, "X-Panel")
		out, _ := json.Marshal(map[string]any{"response": got.Response, "changed": true, "failures": 0})
		return string(out)
	}}
	srv, _ := subEngine(t, h, nil)
	resp, body := rawGet(t, srv.URL+"/sub/abc", func(r *http.Request) { r.Header.Set("Accept-Encoding", "gzip") })
	if resp.StatusCode != 201 || body != "panel:abc+plugin" || resp.Header.Get("X-Plugin") != "1" || resp.Header.Get("X-Panel") != "" {
		t.Errorf("= %d %q %v", resp.StatusCode, body, resp.Header)
	}
	if resp.Header.Get("Content-Security-Policy") != "default-src 'self'" {
		t.Errorf("the panel's own security header was lost: %v", resp.Header)
	}
	if cl := resp.Header.Get("Content-Length"); cl != "" && resp.Header.Get("Content-Encoding") == "" {
		if n, _ := bodyLen(body); cl != n {
			t.Errorf("Content-Length %s for %d bytes", cl, len(body))
		}
	}
	if len(h.postBody) != 1 {
		t.Fatalf("post calls = %d", len(h.postBody))
	}
	for _, managed := range []string{"Content-Length", "Content-Encoding"} {
		if strings.Contains(h.postBody[0], managed) {
			t.Errorf("the plugin was shown the transport header %s", managed)
		}
	}
}

func bodyLen(s string) (string, int) {
	return strings.TrimSpace(strings.Repeat(" ", 0) + itoa(len(s))), len(s)
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestAnUnchangedAnswerIsSentAsThePanelMadeIt(t *testing.T) {
	h := &seamHost{caps: map[string]int{"seams": 1}, seams: subPostOnl, postFn: func(string) string {
		return `{"response":{"status":200},"changed":false,"failures":0}`
	}}
	srv, _ := subEngine(t, h, nil)
	resp, body := rawGet(t, srv.URL+"/sub/abc")
	if resp.StatusCode != 200 || body != "panel:abc" || resp.Header.Get("X-Panel") != "1" {
		t.Errorf("= %d %q %v", resp.StatusCode, body, resp.Header)
	}
}

func TestAFailingOrSlowHostLeavesTheRequestToThePanel(t *testing.T) {
	cases := map[string]*seamHost{
		"error": {caps: map[string]int{"seams": 1}, seams: subSeams, status: http.StatusInternalServerError, postFn: func(string) string { return "{" }},
		"junk":  {caps: map[string]int{"seams": 1}, seams: subSeams, preReply: "not json", postFn: func(string) string { return "not json" }},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			srv, s := subEngine(t, h, nil)
			resp, body := rawGet(t, srv.URL+"/sub/abc?a=1", func(r *http.Request) { r.Header.Set("X-Keep", "1") })
			if resp.StatusCode != 200 || body != "panel:abc" || s.calls.Load() != 1 {
				t.Errorf("= %d %q, handler ran %d times", resp.StatusCode, body, s.calls.Load())
			}
			if s.query != "a=1" || s.header.Get("X-Keep") != "1" {
				t.Errorf("a failed call changed the request: query %q headers %v", s.query, s.header)
			}
		})
	}
	slow := &seamHost{caps: map[string]int{"seams": 1}, seams: subSeams, slow: 500 * time.Millisecond, preReply: `{}`}
	srv, s := subEngine(t, slow, nil)
	Shared().seamTimeout = 100 * time.Millisecond
	start := time.Now()
	resp, body := rawGet(t, srv.URL+"/sub/abc")
	if resp.StatusCode != 200 || body != "panel:abc" || s.calls.Load() != 1 || time.Since(start) > 900*time.Millisecond {
		t.Errorf("slow host: %d %q after %v", resp.StatusCode, body, time.Since(start))
	}
}

func TestOtherRoutesAndStreamsAreLeftAlone(t *testing.T) {
	h := &seamHost{caps: map[string]int{"seams": 1}, seams: subSeams, preReply: `{"responded":true,"response":{"status":500}}`}
	srv, _ := subEngine(t, h, nil)
	if resp, body := rawGet(t, srv.URL+"/other"); resp.StatusCode != 200 || body != "other" {
		t.Errorf("a route nobody resolved = %d %q", resp.StatusCode, body)
	}
	for name, mutate := range map[string]func(*http.Request){
		"an event stream": func(r *http.Request) { r.Header.Set("Accept", "text/event-stream") },
		"an upgrade":      func(r *http.Request) { r.Header.Set("Upgrade", "websocket") },
	} {
		before := len(h.preBody)
		if resp, body := rawGet(t, srv.URL+"/sub/abc", mutate); resp.StatusCode != 200 || body != "panel:abc" || len(h.preBody) != before {
			t.Errorf("%s was offered to seams: %d %q", name, resp.StatusCode, body)
		}
	}
}

func TestACallThePluginSystemMakesItselfNeverPassesThroughASeam(t *testing.T) {
	h := &seamHost{caps: map[string]int{"seams": 1}, seams: subSeams, preReply: `{"responded":true,"response":{"status":500}}`}
	l := startedSeamLink(t, h)
	prev := Shared()
	SetShared(l)
	defer SetShared(prev)
	waitFor(t, func() bool { pre, _ := l.SeamWanted("sub", "GET", "/links/:subid"); return pre })
	e := gin.New()
	e.Use(SeamMiddleware(EngineSub, func(c *gin.Context) string { return "/links/:subid" }))
	e.GET("/sub/:subid", func(c *gin.Context) { c.String(200, "panel") })
	srv := httptest.NewServer(InternalHandler(wrapAPIPrefix(e), func() string { return "" }))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/panel/api/sub/abc", nil)
	req.Header.Set("X-LucX-Allowed-Routes", "GET /x")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || len(h.preBody) != 0 {
		t.Errorf("a plugin's own call = %d, host calls %d: it would loop", resp.StatusCode, len(h.preBody))
	}
}

// wrapAPIPrefix serves engine under /panel/api/ the way the panel does.
func wrapAPIPrefix(e *gin.Engine) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r2 := r.Clone(r.Context())
		r2.URL.Path = strings.TrimPrefix(r.URL.Path, "/panel/api")
		e.ServeHTTP(w, r2)
	})
}

func TestBigRequestsAndBigAnswersGoThroughUntouched(t *testing.T) {
	h := &seamHost{caps: map[string]int{"seams": 1}, seams: subAnyBoth, preReply: `{"responded":true,"response":{"status":500}}`, postFn: func(string) string {
		return `{"response":{"status":500},"changed":true,"failures":0}`
	}}
	srv, s := subEngine(t, h, nil)
	big := strings.Repeat("a", MaxSeamRequestBody+10)
	resp, err := http.Post(srv.URL+"/sub/abc", "text/plain", strings.NewReader(big))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || s.body != big || len(h.preBody) != 0 {
		t.Errorf("a request over the limit: %d, handler saw %d bytes, host calls %d", resp.StatusCode, len(s.body), len(h.preBody))
	}

	h2 := &seamHost{caps: map[string]int{"seams": 1}, seams: subPostOnl, postFn: func(string) string {
		return `{"response":{"status":500},"changed":true,"failures":0}`
	}}
	huge := strings.Repeat("z", MaxSeamResponseBody+5)
	srv2, _ := subEngine(t, h2, func(c *gin.Context) { c.Data(200, "text/plain", []byte(huge)) })
	resp2, body2 := rawGet(t, srv2.URL+"/sub/abc")
	if resp2.StatusCode != 200 || body2 != huge || len(h2.postBody) != 0 {
		t.Errorf("an answer over the limit: %d, %d bytes, host calls %d", resp2.StatusCode, len(body2), len(h2.postBody))
	}
}

func TestThePluginSystemIsNeverShownTheSessionAndGetsTheUserAndParams(t *testing.T) {
	h := &seamHost{caps: map[string]int{"seams": 1}, seams: `[{"engine":"api","route":"/clients/*","pre":true,"post":false}]`, preReply: `{}`}
	l := startedSeamLink(t, h)
	prev := Shared()
	SetShared(l)
	defer SetShared(prev)
	waitFor(t, func() bool { pre, _ := l.SeamWanted("api", "GET", "/clients/get/:email"); return pre })
	e := gin.New()
	e.Use(sessions.Sessions("t", cookie.NewStore([]byte("secret"))))
	e.Use(func(c *gin.Context) { session.SetAPIAuthUser(c, &model.User{Username: "boss"}); c.Next() })
	api := e.Group("/panel/api")
	api.Use(SeamMiddleware(EngineAPI, func(c *gin.Context) string { return relRoute(c.FullPath()) }))
	api.GET("/clients/get/:email", func(c *gin.Context) { c.String(200, "ok") })
	srv := httptest.NewServer(e)
	defer srv.Close()
	rawGet(t, srv.URL+"/panel/api/clients/get/a@x.com?q=1", func(r *http.Request) {
		r.Header.Set("Cookie", "3x-ui=session-secret")
		r.Header.Set("Authorization", "Bearer api-token")
		r.Header.Set("X-Csrf-Token", "csrf-secret")
		r.Header.Set("X-Forwarded-For", "9.9.9.9")
		r.Header.Set("Accept", "application/json")
	})
	if len(h.preBody) != 1 {
		t.Fatalf("host calls = %d", len(h.preBody))
	}
	for _, secret := range []string{"session-secret", "api-token", "csrf-secret", "9.9.9.9"} {
		if strings.Contains(h.preBody[0], secret) {
			t.Errorf("the plugin system was shown %q: %s", secret, h.preBody[0])
		}
	}
	var in SeamRequest
	_ = json.Unmarshal([]byte(h.preBody[0]), &in)
	if in.User != "boss" || in.Params["email"] != "a@x.com" || in.Route != "/clients/get/:email" || in.Engine != "api" || in.Headers["Accept"][0] != "application/json" {
		t.Errorf("request = %+v", in)
	}
}

func relRoute(full string) string {
	_, after, _ := strings.Cut(full, "/panel/api")
	return after
}

func TestAProtectedRouteIsNeverOfferedEvenWhenTheHostAsksForEverything(t *testing.T) {
	h := &seamHost{caps: map[string]int{"seams": 1}, seams: `[{"engine":"api","route":"*","pre":true,"post":true}]`, preReply: `{"responded":true,"response":{"status":500}}`}
	l := startedSeamLink(t, h)
	prev := Shared()
	SetShared(l)
	defer SetShared(prev)
	waitFor(t, func() bool { pre, _ := l.SeamWanted("api", "GET", "/clients/list"); return pre })
	e := gin.New()
	e.Use(sessions.Sessions("t", cookie.NewStore([]byte("secret"))))
	api := e.Group("/panel/api")
	api.Use(SeamMiddleware(EngineAPI, func(c *gin.Context) string { return relRoute(c.FullPath()) }))
	api.POST("/setting/updateUser", func(c *gin.Context) { c.String(200, "changed") })
	api.GET("/server/getDb", func(c *gin.Context) { c.String(200, "db") })
	api.GET("/clients/list", func(c *gin.Context) { c.String(200, "list") })
	srv := httptest.NewServer(e)
	defer srv.Close()
	post := func(path string) int {
		resp, err := http.Post(srv.URL+path, "text/plain", strings.NewReader(""))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if post("/panel/api/setting/updateUser") != 200 {
		t.Error("a plugin took part in changing the panel user")
	}
	if resp, body := rawGet(t, srv.URL+"/panel/api/server/getDb"); resp.StatusCode != 200 || body != "db" {
		t.Error("a plugin took part in the database download")
	}
	if resp, _ := rawGet(t, srv.URL+"/panel/api/clients/list"); resp.StatusCode != 500 {
		t.Error("the seam on an ordinary route was not used: the test would prove nothing")
	}
}

func TestHeadRequestsGetNoBodyEvenWhenAPluginAnswers(t *testing.T) {
	h := &seamHost{caps: map[string]int{"seams": 1}, seams: subSeams, preReply: `{"responded":true,"response":{"status":200,"body":"` + b64("hidden") + `"}}`}
	srv, _ := subEngine(t, h, nil)
	req, _ := http.NewRequest(http.MethodHead, srv.URL+"/sub/abc", nil)
	resp, err := (&http.Client{Transport: &http.Transport{DisableCompression: true}}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || len(raw) != 0 {
		t.Errorf("HEAD = %d with %d body bytes", resp.StatusCode, len(raw))
	}
}

func TestAPluginCannotPutSessionOrSecurityHeadersIntoEitherDirection(t *testing.T) {
	h := &seamHost{caps: map[string]int{"seams": 1}, seams: subSeams,
		preFn: func(in string) string {
			var req SeamRequest
			_ = json.Unmarshal([]byte(in), &req)
			req.Headers["Cookie"] = []string{"3x-ui=stolen"}
			req.Headers["Authorization"] = []string{"Bearer forged"}
			req.Headers["X-Forwarded-For"] = []string{"6.6.6.6"}
			req.Headers["X-Lucx-Allowed-Routes"] = []string{"GET /anything"}
			out, _ := json.Marshal(map[string]any{"responded": false, "request": req, "response": map[string]any{}, "failures": 0})
			return string(out)
		},
		postFn: func(in string) string {
			var got struct{ Response SeamResponse }
			_ = json.Unmarshal([]byte(in), &got)
			got.Response.Headers["Set-Cookie"] = []string{"a=b"}
			got.Response.Headers["Content-Security-Policy"] = []string{"default-src *"}
			got.Response.Headers["Access-Control-Allow-Origin"] = []string{"*"}
			got.Response.Headers["Content-Encoding"] = []string{"br"}
			got.Response.Headers["X-Fine"] = []string{"1"}
			out, _ := json.Marshal(map[string]any{"response": got.Response, "changed": true, "failures": 0})
			return string(out)
		}}
	srv, s := subEngine(t, h, nil)
	resp, body := rawGet(t, srv.URL+"/sub/abc")
	for _, name := range []string{"Cookie", "Authorization", "X-Forwarded-For", "X-Lucx-Allowed-Routes"} {
		if s.header.Get(name) != "" {
			t.Errorf("the handler received %s from a plugin", name)
		}
	}
	if resp.Header.Get("Set-Cookie") != "" || resp.Header.Get("Access-Control-Allow-Origin") != "" || resp.Header.Get("Content-Security-Policy") != "default-src 'self'" {
		t.Errorf("a plugin touched the panel's security headers: %v", resp.Header)
	}
	if resp.Header.Get("X-Fine") != "1" || body != "panel:abc" {
		t.Errorf("an allowed header was dropped or the body was damaged: %q %v", body, resp.Header)
	}
}

func TestNoContentAnswersCarryNoBody(t *testing.T) {
	h := &seamHost{caps: map[string]int{"seams": 1}, seams: subPreOnly, preReply: `{"responded":true,"response":{"status":204,"body":"` + b64("x") + `"}}`}
	srv, _ := subEngine(t, h, nil)
	if resp, body := rawGet(t, srv.URL+"/sub/abc"); resp.StatusCode != 204 || body != "" {
		t.Errorf("= %d %q", resp.StatusCode, body)
	}
}

func TestAChunkedBodyOverTheLimitIsLeftWhole(t *testing.T) {
	h := &seamHost{caps: map[string]int{"seams": 1}, seams: subAnyBoth, preReply: `{"responded":true,"response":{"status":500}}`}
	srv, s := subEngine(t, h, nil)
	big := strings.Repeat("c", MaxSeamRequestBody+10)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/sub/abc", io.NopCloser(strings.NewReader(big)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || s.body != big || len(h.preBody) != 0 {
		t.Errorf("chunked: %d, handler saw %d bytes, host calls %d", resp.StatusCode, len(s.body), len(h.preBody))
	}
}

// What net/http would hide, checked on a bare recorder.
func TestNoBodyIsWrittenForHeadOrBodylessStatuses(t *testing.T) {
	for name, tc := range map[string]struct {
		method string
		reply  string
		pre    bool
	}{
		"head, plugin answers": {http.MethodHead, `{"responded":true,"response":{"status":200,"body":"` + b64("x") + `"}}`, true},
		"204, plugin answers":  {http.MethodGet, `{"responded":true,"response":{"status":204,"body":"` + b64("x") + `"}}`, true},
		"304, plugin answers":  {http.MethodGet, `{"responded":true,"response":{"status":304,"body":"` + b64("x") + `"}}`, true},
	} {
		t.Run(name, func(t *testing.T) {
			h := &seamHost{caps: map[string]int{"seams": 1}, seams: subSeams, preReply: tc.reply}
			l := startedSeamLink(t, h)
			prev := Shared()
			SetShared(l)
			defer SetShared(prev)
			waitFor(t, func() bool { pre, _ := l.SeamWanted("sub", "GET", "/links/:subid"); return pre })
			e := gin.New()
			e.Use(SeamMiddleware(EngineSub, func(*gin.Context) string { return "/links/:subid" }))
			e.Any("/sub/:subid", func(c *gin.Context) { c.String(200, "panel") })
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, httptest.NewRequest(tc.method, "/sub/abc", nil))
			if rec.Body.Len() != 0 {
				t.Errorf("body %q written", rec.Body.String())
			}
		})
	}
	// and the panel's own answer after a changing plugin
	for name, method := range map[string]string{"head": http.MethodHead} {
		t.Run("post "+name, func(t *testing.T) {
			h := &seamHost{caps: map[string]int{"seams": 1}, seams: subPostOnl, postFn: func(string) string {
				return `{"response":{"status":200,"body":"` + b64("x") + `"},"changed":true,"failures":0}`
			}}
			l := startedSeamLink(t, h)
			prev := Shared()
			SetShared(l)
			defer SetShared(prev)
			waitFor(t, func() bool { _, post := l.SeamWanted("sub", "GET", "/links/:subid"); return post })
			e := gin.New()
			e.Use(SeamMiddleware(EngineSub, func(*gin.Context) string { return "/links/:subid" }))
			e.Any("/sub/:subid", func(c *gin.Context) { c.String(200, "panel") })
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, httptest.NewRequest(method, "/sub/abc", nil))
			if rec.Body.Len() != 0 {
				t.Errorf("body %q written", rec.Body.String())
			}
		})
	}
}
