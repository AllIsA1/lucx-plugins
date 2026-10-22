// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/eventbus"
)

// pluginSocket serves handler on socketDir/<id>.sock and records requests.
type pluginSocket struct {
	mu   sync.Mutex
	seen []seenRequest
}

func listenPlugin(t *testing.T, sockDir, id string, handler func(w http.ResponseWriter, r *http.Request, body []byte)) *pluginSocket {
	t.Helper()
	ps := &pluginSocket{}
	ln, err := net.Listen("unix", filepath.Join(sockDir, id+".sock"))
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := ioReadAll(r)
		ps.mu.Lock()
		ps.seen = append(ps.seen, seenRequest{r.Method, r.URL.Path, r.URL.RawQuery, string(body), r.Header.Clone()})
		ps.mu.Unlock()
		if handler != nil {
			handler(w, r, body)
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ps
}

func ioReadAll(r *http.Request) ([]byte, error) {
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := r.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return []byte(b.String()), nil
}

func (p *pluginSocket) requests(path string) []seenRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []seenRequest
	for _, r := range p.seen {
		if r.Path == path {
			out = append(out, r)
		}
	}
	return out
}

// eventEnv: one running plugin holding the given permissions, served by handler.
func eventEnv(t *testing.T, perms []string, handler func(w http.ResponseWriter, r *http.Request, body []byte), opts ...func(*SupervisorOptions)) (*supEnv, *pluginSocket) {
	t.Helper()
	sockDir, err := os.MkdirTemp("", "lucxe")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	e := newSupEnv(t, func(m map[string]any) { m["permissions"] = perms }, opts...)
	t.Cleanup(e.sup.Close)
	e.runner.socketDir = sockDir
	e.enable(t)
	e.reconcile()
	return e, listenPlugin(t, sockDir, "traffic-alerts", handler)
}

func clientCreated(email string) eventbus.Event {
	return eventbus.Event{Type: eventbus.EventClientCreated, Source: email, Data: eventbus.ClientEventData{Email: email, InboundIDs: []int{3}}}
}

func TestDispatchDeliversSubscribedEventsOnly(t *testing.T) {
	e, plug := eventEnv(t, []string{"events:client.created", "api:clients:read"}, nil)

	e.sup.Dispatch(clientCreated("alice@example.com"))
	e.sup.Dispatch(eventbus.Event{Type: eventbus.EventClientDeleted, Source: "bob@example.com"})
	waitFor(t, "the subscribed event", func() bool { return len(plug.requests("/_lucx/events")) == 1 })
	time.Sleep(50 * time.Millisecond)

	reqs := plug.requests("/_lucx/events")
	if len(reqs) != 1 {
		t.Fatalf("the plugin received %d events, want only client.created", len(reqs))
	}
	var got struct {
		ID     string
		Type   string
		Source string
		Time   time.Time
		Data   struct {
			Email      string
			InboundIDs []int
		}
	}
	if err := json.Unmarshal([]byte(reqs[0].Body), &got); err != nil {
		t.Fatalf("payload is not JSON: %v: %s", err, reqs[0].Body)
	}
	if got.Type != "client.created" || got.Source != "alice@example.com" || got.Data.Email != "alice@example.com" ||
		len(got.Data.InboundIDs) != 1 || got.ID == "" || got.Time.IsZero() {
		t.Errorf("payload = %+v", got)
	}
	if reqs[0].Method != http.MethodPost || reqs[0].Header.Get("X-Lucx-Event") != "client.created" || reqs[0].Header.Get("Content-Type") != "application/json" {
		t.Errorf("request = %s %v", reqs[0].Method, reqs[0].Header)
	}
}

func TestDispatchSkipsPluginsWithoutTheRightOrNotRunning(t *testing.T) {
	none, plug := eventEnv(t, []string{"api:clients:read"}, nil)
	none.sup.Dispatch(clientCreated("a@example.com"))
	time.Sleep(100 * time.Millisecond)
	if n := len(plug.seen); n != 0 {
		t.Errorf("a plugin without any events: permission received %d requests", n)
	}

	stopped, plug2 := eventEnv(t, []string{"events:client.created"}, nil)
	stopped.runner.units["traffic-alerts"].state = StateStopping
	stopped.reconcile()
	stopped.sup.Dispatch(clientCreated("a@example.com"))
	time.Sleep(100 * time.Millisecond)
	if n := len(plug2.seen); n != 0 {
		t.Errorf("a plugin that is not running received %d requests", n)
	}
	rt, _ := stopped.sup.Runtime("traffic-alerts")
	if rt.EventsDropped != 1 {
		t.Errorf("EventsDropped = %d, want 1 for the undeliverable event", rt.EventsDropped)
	}
}

func TestDispatchKeepsOrderAndCountsFailures(t *testing.T) {
	var mu sync.Mutex
	fail := true
	e, plug := eventEnv(t, []string{"events:client.created"}, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		mu.Lock()
		defer mu.Unlock()
		if fail {
			http.Error(w, "boom", http.StatusInternalServerError)
		}
	})
	e.sup.Dispatch(clientCreated("one@example.com"))
	waitFor(t, "the failing delivery", func() bool { rt, _ := e.sup.Runtime("traffic-alerts"); return rt.EventsFailed == 1 })
	mu.Lock()
	fail = false
	mu.Unlock()
	for i := 2; i <= 4; i++ {
		e.sup.Dispatch(clientCreated(fmt.Sprintf("n%d@example.com", i)))
	}
	waitFor(t, "later events after a failure", func() bool { rt, _ := e.sup.Runtime("traffic-alerts"); return rt.EventsSent == 3 })

	var order []string
	for _, r := range plug.requests("/_lucx/events") {
		var ev struct{ Source string }
		_ = json.Unmarshal([]byte(r.Body), &ev)
		order = append(order, ev.Source)
	}
	want := "one@example.com n2@example.com n3@example.com n4@example.com"
	if strings.Join(order, " ") != want {
		t.Errorf("delivery order = %v", order)
	}
	if rt, _ := e.sup.Runtime("traffic-alerts"); rt.LastError == "" {
		t.Error("the failure must be visible in the runtime")
	}
}

func TestDispatchNeverBlocksOnASlowPlugin(t *testing.T) {
	release := make(chan struct{})
	e, _ := eventEnv(t, []string{"events:client.created"}, func(http.ResponseWriter, *http.Request, []byte) { <-release })
	t.Cleanup(func() { close(release) })

	done := make(chan struct{})
	go func() {
		for i := 0; i < eventQueueSize*3; i++ {
			e.sup.Dispatch(clientCreated("a@example.com"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Dispatch blocked on a stuck plugin; the bus handler must never wait for plugins")
	}
	if rt, _ := e.sup.Runtime("traffic-alerts"); rt.EventsDropped == 0 {
		t.Error("events beyond the queue must be counted as dropped")
	}
}

func TestWantedReflectsEnabledSubscribers(t *testing.T) {
	e, _ := eventEnv(t, []string{"events:client.created"}, nil)
	if !e.sup.Wanted("client.created") || e.sup.Wanted("client.deleted") {
		t.Error("Wanted must match exactly the subscribed events")
	}
	if err := e.m.SetEnabled("traffic-alerts", false); err != nil {
		t.Fatal(err)
	}
	if e.sup.Wanted("client.created") {
		t.Error("a disabled plugin wants nothing")
	}
}

var _ = context.Background
