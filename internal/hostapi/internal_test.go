// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AllIsA1/lucx-plugins/internal/plugins"
)

type internalFixture struct {
	srv        *httptest.Server
	events     []plugins.Event
	transforms []string
	validate   func(before, after []byte) error
}

func newInternalFixture(t *testing.T) *internalFixture {
	t.Helper()
	f := &internalFixture{}
	hooks := Hooks{
		Version:  "test",
		Dispatch: func(ev plugins.Event) { f.events = append(f.events, ev) },
		Transform: func(_ context.Context, name string, doc []byte, validate func(before, after []byte) error) []byte {
			f.transforms = append(f.transforms, name)
			f.validate = validate
			return append(doc, ' ')
		},
		Subscriptions: func() ([]string, []string) { return []string{"client.created"}, []string{"xray.config"} },
	}
	srv := NewServer(&PluginService{Provider: func() (*plugins.Manager, error) { return nil, plugins.ErrDisabled }})
	srv.SetHooks(hooks)
	f.srv = httptest.NewServer(srv.Handler())
	t.Cleanup(f.srv.Close)
	return f
}

func (f *internalFixture) do(method, path, body string) (int, string) {
	req, _ := http.NewRequest(method, f.srv.URL+path, strings.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func TestInternalHelloNamesTheContractVersion(t *testing.T) {
	f := newInternalFixture(t)
	code, body := f.do(http.MethodGet, "/internal/hello", "")
	var hello struct {
		IntegrationAPI int    `json:"integrationApi"`
		Host           string `json:"host"`
	}
	if err := json.Unmarshal([]byte(body), &hello); err != nil || code != 200 || hello.IntegrationAPI != IntegrationAPI || hello.Host != "test" {
		t.Errorf("hello = %d %q (%v)", code, body, err)
	}
}

func TestInternalEventsAreHandedToThePlugins(t *testing.T) {
	f := newInternalFixture(t)
	code, _ := f.do(http.MethodPost, "/internal/events", `{"type":"client.created","source":"a@b.c","data":{"email":"a@b.c"},"timestamp":"2026-10-22T10:00:00Z"}`)
	if code != http.StatusAccepted {
		t.Fatalf("status %d, want 202: the panel must not wait for plugins", code)
	}
	if len(f.events) != 1 || f.events[0].Type != plugins.EventClientCreated || f.events[0].Source != "a@b.c" {
		t.Fatalf("events = %+v", f.events)
	}
	if !f.events[0].Timestamp.Equal(time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("timestamp = %v", f.events[0].Timestamp)
	}
	if code, _ := f.do(http.MethodPost, "/internal/events", "{broken"); code != http.StatusBadRequest {
		t.Errorf("a malformed event = %d, want 400", code)
	}
	if code, _ := f.do(http.MethodPost, "/internal/events", strings.Repeat(" ", maxEventBody+1)); code != http.StatusRequestEntityTooLarge {
		t.Errorf("an oversized event = %d, want 413", code)
	}
	if len(f.events) != 1 {
		t.Errorf("bad events were dispatched: %d", len(f.events))
	}
}

func TestInternalPipelineReturnsTheTransformedDocumentWithTheRightValidator(t *testing.T) {
	f := newInternalFixture(t)
	code, body := f.do(http.MethodPost, "/internal/pipeline/xray.config", baseXrayDoc)
	if code != 200 || body != baseXrayDoc+" " {
		t.Fatalf("pipeline answer = %d %.40q", code, body)
	}
	if len(f.transforms) != 1 || f.transforms[0] != "xray.config" {
		t.Fatalf("transforms = %v", f.transforms)
	}
	// The host guards what the panel depends on between plugins, with the Xray rules.
	bad := edit(t, baseXrayDoc, func(m map[string]any) { delete(m, "stats") })
	if f.validate == nil || f.validate([]byte(baseXrayDoc), []byte(bad)) == nil {
		t.Error("xray.config must be validated with the Xray rules")
	}
	if code, _ := f.do(http.MethodPost, "/internal/pipeline/nonsense", "{}"); code != http.StatusNotFound {
		t.Errorf("an unknown pipeline = %d, want 404", code)
	}
}

func TestInternalSubscriptionsTellThePanelWhatToSend(t *testing.T) {
	f := newInternalFixture(t)
	code, body := f.do(http.MethodGet, "/internal/subscriptions", "")
	if code != 200 || !strings.Contains(body, `"events":["client.created"]`) || !strings.Contains(body, `"pipelines":["xray.config"]`) {
		t.Errorf("subscriptions = %d %s", code, body)
	}
}
