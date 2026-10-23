// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostlink

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/eventbus"
	"github.com/mhsanaei/3x-ui/v3/internal/lucx/nodetype"
)

type hostState struct {
	mu        sync.Mutex
	api       int
	events    []string
	pipelines []string
	received  [][]byte
	pipeline  func(http.ResponseWriter, []byte)
}

func (s *hostState) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch r.URL.Path {
		case "/internal/hello":
			_ = json.NewEncoder(w).Encode(map[string]any{"integrationApi": s.api, "host": "9.9"})
		case "/internal/subscriptions":
			_ = json.NewEncoder(w).Encode(map[string]any{"events": s.events, "pipelines": s.pipelines})
		case "/internal/events":
			b, _ := io.ReadAll(r.Body)
			s.received = append(s.received, b)
			w.WriteHeader(http.StatusAccepted)
		case "/internal/pipeline/xray.config":
			b, _ := io.ReadAll(r.Body)
			s.received = append(s.received, b)
			s.pipeline(w, b)
		default:
			http.NotFound(w, r)
		}
	})
}

func (s *hostState) got() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte(nil), s.received...)
}

func started(t *testing.T, s *hostState) *Link {
	t.Helper()
	l := newLink(t, fakeHost(t, s.handler()))
	l.Start()
	waitFor(t, func() bool { return l.Status().Connected })
	return l
}

func TestHandshakeLoadsWhatPluginsSubscribedTo(t *testing.T) {
	s := &hostState{api: IntegrationAPI, events: []string{"client.created"}, pipelines: []string{"xray.config"}}
	l := started(t, s)
	waitFor(t, func() bool { return l.Wanted("client.created") })
	if l.Wanted("client.deleted") {
		t.Error("an event nobody subscribed to is wanted")
	}
	if !l.HasPipeline("xray.config") || l.HasPipeline("other") {
		t.Error("pipeline subscriptions were not loaded")
	}
	if st := l.Status(); !st.Compatible || st.HostVersion != "9.9" || st.Reason != "" {
		t.Errorf("status = %+v", st)
	}
}

func TestAnIncompatibleHostIsNotUsedAndTheReasonSaysWhatToUpdate(t *testing.T) {
	s := &hostState{api: IntegrationAPI + 1, events: []string{"client.created"}, pipelines: []string{"xray.config"}}
	l := started(t, s)
	if l.Wanted("client.created") || l.HasPipeline("xray.config") {
		t.Error("subscriptions of an incompatible host were honoured")
	}
	if st := l.Status(); st.Compatible || st.Reason == "" {
		t.Errorf("status = %+v, want incompatible with a reason", st)
	}
}

func TestWhenTheHostGoesAwayTheSubscriptionsGoWithIt(t *testing.T) {
	l := newLink(t, socketPath(t, "gone.sock"))
	l.subs.Store(&subscriptions{events: map[string]struct{}{"client.created": {}}, pipelines: map[string]struct{}{"xray.config": {}}})
	l.Refresh(context.Background()) // nothing listens
	if st := l.Status(); st.Connected || st.Reason == "" {
		t.Errorf("status = %+v, want not connected with a reason", st)
	}
	if l.Wanted("client.created") || l.HasPipeline("xray.config") {
		t.Error("subscriptions outlived the host: events would be queued for nobody")
	}
}

func TestAnEventReachesTheHostOnlyWhenWanted(t *testing.T) {
	s := &hostState{api: IntegrationAPI, events: []string{"client.created"}}
	l := started(t, s)
	waitFor(t, func() bool { return l.Wanted("client.created") })

	l.HandleEvent(eventbus.Event{Type: "client.deleted", Source: "ignored@x"})
	l.HandleEvent(eventbus.Event{Type: "client.created", Source: "a@x", Timestamp: time.Unix(1700000000, 0).UTC(),
		Data: eventbus.ClientEventData{Email: "a@x", InboundIDs: []int{3}}})
	waitFor(t, func() bool { return len(s.got()) == 1 })

	var ev struct {
		Type, Source string
		Data         struct {
			Email      string
			InboundIDs []int `json:"inboundIds"`
		}
		Timestamp time.Time
	}
	if err := json.Unmarshal(s.got()[0], &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Type != "client.created" || ev.Source != "a@x" || ev.Data.Email != "a@x" || len(ev.Data.InboundIDs) != 1 || ev.Timestamp.Unix() != 1700000000 {
		t.Errorf("host received %s", s.got()[0])
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(s.got()); n != 1 {
		t.Errorf("host received %d events, want only the wanted one", n)
	}
}

func TestAFullQueueDropsEventsInsteadOfBlockingTheBus(t *testing.T) {
	s := &hostState{api: IntegrationAPI, events: []string{"client.created"}}
	l := newLink(t, fakeHost(t, s.handler()))
	l.Refresh(context.Background())
	// No sender is running, so the queue can only fill up.
	done := make(chan struct{})
	go func() {
		for i := 0; i < eventQueueSize*3; i++ {
			l.HandleEvent(eventbus.Event{Type: "client.created"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("HandleEvent blocked on a full queue")
	}
	if len(l.events) != eventQueueSize {
		t.Errorf("queue holds %d, want %d", len(l.events), eventQueueSize)
	}
}

func TestSendingToADeadHostNeitherPanicsNorHangs(t *testing.T) {
	l := newLink(t, socketPath(t, "dead.sock"))
	l.subs.Store(&subscriptions{events: map[string]struct{}{"client.created": {}}})
	l.Start()
	l.HandleEvent(eventbus.Event{Type: "client.created"})
	time.Sleep(100 * time.Millisecond)
	l.Stop()
}

func TestStopThenStartWorksAgain(t *testing.T) {
	s := &hostState{api: IntegrationAPI, events: []string{"client.created"}}
	l := started(t, s)
	l.Stop()
	if l.Status().Connected || l.Wanted("client.created") {
		t.Error("a stopped link still claims a host")
	}
	l.Start()
	waitFor(t, func() bool { return l.Wanted("client.created") })
}

func TestTransformReturnsThePatchedDocument(t *testing.T) {
	s := &hostState{api: IntegrationAPI, pipelines: []string{"xray.config"}}
	s.pipeline = func(w http.ResponseWriter, in []byte) { _, _ = w.Write(append(in, []byte(" patched")...)) }
	l := started(t, s)
	waitFor(t, func() bool { return l.HasPipeline("xray.config") })
	if got := l.Transform(context.Background(), "xray.config", []byte("doc")); string(got) != "doc patched" {
		t.Errorf("Transform = %q", got)
	}
}

func TestTransformFailsOpen(t *testing.T) {
	cases := map[string]func(http.ResponseWriter, []byte){
		"host error": func(w http.ResponseWriter, _ []byte) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("internal error page"))
		},
		"empty": func(w http.ResponseWriter, _ []byte) {},
		"too slow": func(w http.ResponseWriter, _ []byte) {
			time.Sleep(500 * time.Millisecond)
			_, _ = w.Write([]byte("late"))
		},
	}
	old := pipelineBudget
	pipelineBudget = 100 * time.Millisecond
	defer func() { pipelineBudget = old }()
	for name, reply := range cases {
		t.Run(name, func(t *testing.T) {
			s := &hostState{api: IntegrationAPI, pipelines: []string{"xray.config"}, pipeline: reply}
			l := started(t, s)
			waitFor(t, func() bool { return l.HasPipeline("xray.config") })
			if got := l.Transform(context.Background(), "xray.config", []byte("doc")); string(got) != "doc" {
				t.Errorf("Transform = %q, want the original document", got)
			}
		})
	}
}

func TestTransformWithoutAPluginDoesNotEvenCallTheHost(t *testing.T) {
	s := &hostState{api: IntegrationAPI}
	s.pipeline = func(w http.ResponseWriter, in []byte) { _, _ = w.Write([]byte("changed")) }
	l := started(t, s)
	if got := l.Transform(context.Background(), "xray.config", []byte("doc")); string(got) != "doc" {
		t.Errorf("Transform = %q", got)
	}
	if n := len(s.got()); n != 0 {
		t.Errorf("host was called %d times", n)
	}
}

func TestTransformToADeadHostKeepsTheDocument(t *testing.T) {
	l := newLink(t, socketPath(t, "dead.sock"))
	l.subs.Store(&subscriptions{pipelines: map[string]struct{}{"xray.config": {}}})
	if got := l.Transform(context.Background(), "xray.config", []byte("doc")); string(got) != "doc" {
		t.Errorf("Transform = %q", got)
	}
}

func TestAHostThatStartsLaterIsFoundQuickly(t *testing.T) {
	old := retryInterval
	retryInterval = 30 * time.Millisecond
	defer func() { retryInterval = old }()

	sock := socketPath(t, "late.sock")
	l := newLink(t, sock)
	l.Start()
	time.Sleep(100 * time.Millisecond) // several failed handshakes
	if l.Status().Connected {
		t.Fatal("connected to nothing")
	}
	s := &hostState{api: IntegrationAPI, events: []string{"client.created"}}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: s.handler()}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	waitFor(t, func() bool { return l.Wanted("client.created") })
}

func TestAvailableMeansAHostAnsweredRecentlyAndIsNotShakenByARestart(t *testing.T) {
	s := &hostState{api: IntegrationAPI}
	l := started(t, s)
	if !l.Available() {
		t.Fatal("a host that answers is not available")
	}
	// The host goes away for a moment (a self-update restarts it): still available within the grace.
	l.Refresh(context.Background())
	l.setStatus(Status{Reason: "the plugin system service is not running"})
	if !l.Available() {
		t.Error("a host that was there seconds ago looks gone: masters would see the node flap")
	}
	l.lastOK.Store(time.Now().Add(-2 * availableGrace).Unix())
	if l.Available() {
		t.Error("a host that has been gone for minutes is still announced")
	}
	if New(Config{HostSocket: socketPath(t, "x.sock")}).Available() {
		t.Error("a link that never met a host is available")
	}
	l.Stop()
	if l.Available() {
		t.Error("a stopped link is available")
	}
}

func TestTheNodeAnnouncesPluginsOnlyForAReachableHost(t *testing.T) {
	s := &hostState{api: IntegrationAPI}
	l := started(t, s)
	prev := Shared()
	SetShared(l)
	defer SetShared(prev)
	has := func() bool { return slices.Contains(nodetype.LocalHello().Features, nodetype.FeaturePlugins) }
	if !has() {
		t.Error("a node with a reachable host does not announce plugins")
	}
	l.Stop()
	if has() {
		t.Error("a node without a host announces plugins")
	}
}
