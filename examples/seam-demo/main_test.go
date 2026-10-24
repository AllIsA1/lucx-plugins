// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func subAfter(body string) SeamCall {
	var c SeamCall
	c.Seam.Engine, c.Seam.Mode, c.Seam.Route = "sub", "after", "/links/:subid"
	c.Request = SeamRequest{Method: "GET", Route: "/links/:subid", Path: "/sub/abc"}
	c.Response = &SeamResponse{Status: 200, Body: []byte(body)}
	return c
}

func TestAddsALineToTheSubscription(t *testing.T) {
	original := "vless://a#one\nvless://b#two"
	reply, err := onSeam(subAfter(base64.StdEncoding.EncodeToString([]byte(original))))
	if err != nil || reply.Action != "modify" || reply.Response == nil || reply.Response.Body == nil {
		t.Fatalf("reply = %+v, err %v", reply, err)
	}
	decoded, err := base64.StdEncoding.DecodeString(string(*reply.Response.Body))
	if err != nil {
		t.Fatalf("the changed subscription is not base64 any more: %v", err)
	}
	if string(decoded) != original+"\n"+subscriptionLine {
		t.Errorf("subscription = %q", decoded)
	}
}

func TestLeavesWhatItDoesNotUnderstandAlone(t *testing.T) {
	for name, c := range map[string]SeamCall{
		"not base64":   subAfter("<html>page</html>"),
		"an error":     func() SeamCall { c := subAfter("Zm9v"); c.Response.Status = 404; return c }(),
		"empty":        subAfter(""),
		"no response":  func() SeamCall { c := subAfter("Zm9v"); c.Response = nil; return c }(),
		"already done": subAfter(base64.StdEncoding.EncodeToString([]byte("vless://a\n" + subscriptionLine))),
	} {
		reply, err := onSeam(c)
		if err != nil || reply.Action != "keep" {
			t.Errorf("%s: reply = %+v, err %v: the panel's answer must stay as it is", name, reply, err)
		}
	}
}

func apiCall(mode, query string) SeamCall {
	var c SeamCall
	c.Seam.Engine, c.Seam.Mode, c.Seam.Route = "api", mode, "/server/status"
	c.Request = SeamRequest{Method: "GET", Route: "/server/status", Path: "/panel/api/server/status", Query: query}
	if mode == "after" {
		c.Response = &SeamResponse{Status: 200, Body: []byte(`{"success":true}`)}
	}
	return c
}

func TestAnswersTheTeapotRequestInsteadOfThePanel(t *testing.T) {
	reply, err := onSeam(apiCall("before", "teapot=1"))
	if err != nil || reply.Action != "respond" || *reply.Response.Status != http.StatusTeapot || !strings.Contains(string(*reply.Response.Body), "teapot") {
		t.Fatalf("reply = %+v, err %v", reply, err)
	}
	if reply, _ := onSeam(apiCall("before", "x=1")); reply.Action != "continue" || reply.Request != nil {
		t.Errorf("an ordinary request must go on unchanged: %+v", reply)
	}
}

func TestMarksTheStatusAnswerWithAHeader(t *testing.T) {
	reply, err := onSeam(apiCall("after", ""))
	if err != nil || reply.Action != "modify" || reply.Response.Headers["X-Seam-Demo"][0] != "1" || reply.Response.Body != nil || reply.Response.Status != nil {
		t.Fatalf("reply = %+v, err %v: only the header may change", reply, err)
	}
}

func TestAnUnknownSeamIsAnError(t *testing.T) {
	c := apiCall("replace", "")
	other := subAfter("")
	other.Seam.Mode = "before"
	if _, err := onSeam(other); err == nil {
		t.Error("a subscription \"before\" seam was never declared")
	}
	if _, err := onSeam(c); err == nil {
		t.Error("a mode the plugin never declared must fail, so the panel falls back to its own code")
	}
}

func TestServesTheSeamEndpoint(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("POST /_lucx/seam/{mode}", SeamHandler(onSeam))
	body, _ := json.Marshal(apiCall("before", "teapot=1"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/_lucx/seam/before", strings.NewReader(string(body))))
	var reply SeamReply
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &reply) != nil || reply.Action != "respond" {
		t.Fatalf("reply = %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/_lucx/seam/before", strings.NewReader("{")))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a broken call = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/_lucx/seam/replace", strings.NewReader(string(mustJSON(apiCall("replace", ""))))))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("an error of the plugin must reach the panel as a failure: %d", rec.Code)
	}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
