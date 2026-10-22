// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/eventbus"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
)

const (
	eventQueueSize = 64
	eventTimeout   = 5 * time.Second
	eventsPath     = reservedPrefix + "/events"
)

type pluginStats struct {
	sent, failed, dropped, pipelineErrors int
	lastError                             string
}

type eventJob struct {
	typ  string
	body []byte
}

type eventPayload struct {
	ID     string    `json:"id"`
	Type   string    `json:"type"`
	Source string    `json:"source"`
	Time   time.Time `json:"time"`
	Data   any       `json:"data"`
}

// newSocketClient talks HTTP over a plugin's unix socket.
func newSocketClient(socket string, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socket)
			},
		},
	}
}

func (s *Supervisor) bump(id string, f func(*pluginStats)) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	st := s.stats[id]
	if st == nil {
		st = &pluginStats{}
		s.stats[id] = st
	}
	f(st)
}

// Wanted reports whether any enabled process plugin subscribes to the event,
// so callers can skip building a payload nobody will read.
func (s *Supervisor) Wanted(event string) bool {
	for _, e := range s.mgr.List() {
		if wantsProcess(e) && subscribed(e, event) {
			return true
		}
	}
	return false
}

// Dispatch queues a panel event for every running plugin that subscribed to
// it. It never blocks: a plugin that cannot keep up loses events, counted in
// its runtime, and never slows the panel.
func (s *Supervisor) Dispatch(ev eventbus.Event) {
	typ := string(ev.Type)
	if !IsKnownEvent(typ) {
		return
	}
	var targets []Entry
	for _, e := range s.mgr.List() {
		if wantsProcess(e) && subscribed(e, typ) {
			targets = append(targets, e)
		}
	}
	if len(targets) == 0 {
		return
	}
	ts := ev.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	body, err := json.Marshal(eventPayload{ID: newRequestID(), Type: typ, Source: ev.Source, Time: ts.UTC(), Data: ev.Data})
	if err != nil {
		logger.Warning("plugins: cannot encode event", typ, err)
		return
	}
	for _, e := range targets {
		if rt, _ := s.Runtime(e.ID); rt.State != StateRunning {
			s.bump(e.ID, func(st *pluginStats) { st.dropped++ })
			continue
		}
		q := s.queueFor(e.ID)
		if q == nil {
			continue
		}
		select {
		case q <- eventJob{typ: typ, body: body}:
		default:
			s.bump(e.ID, func(st *pluginStats) { st.dropped++ })
		}
	}
}

func (s *Supervisor) queueFor(id string) chan eventJob {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	if s.closed {
		return nil
	}
	if q, ok := s.queues[id]; ok {
		return q
	}
	q := make(chan eventJob, eventQueueSize)
	s.queues[id] = q
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		for job := range q {
			s.deliver(id, job)
		}
	}()
	return q
}

func (s *Supervisor) deliver(id string, job eventJob) {
	socket, _ := s.runner.Paths(id)
	ctx, cancel := context.WithTimeout(context.Background(), eventTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://plugin"+eventsPath, bytes.NewReader(job.body))
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-LucX-Event", job.typ)
		var resp *http.Response
		if resp, err = newSocketClient(socket, eventTimeout).Do(req); err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
			_ = resp.Body.Close()
			if resp.StatusCode/100 != 2 {
				err = fmt.Errorf("plugin answered HTTP %d", resp.StatusCode)
			}
		}
	}
	if err != nil {
		s.bump(id, func(st *pluginStats) { st.failed++; st.lastError = "event " + job.typ + ": " + err.Error() })
		return
	}
	s.bump(id, func(st *pluginStats) { st.sent++ })
}

// Close stops event delivery after queued events were attempted. Plugin
// processes keep running. Safe to call more than once.
func (s *Supervisor) Close() {
	s.queueMu.Lock()
	if !s.closed {
		s.closed = true
		for _, q := range s.queues {
			close(q)
		}
	}
	s.queueMu.Unlock()
	s.workers.Wait()
}

// HandleEvent is the event bus subscriber that feeds plugins.
func HandleEvent(ev eventbus.Event) {
	if s := GetSupervisor(); s != nil {
		s.Dispatch(ev)
	}
}

// Wanted reports whether a running plugin subscribes to the event.
func Wanted(event string) bool {
	if s := GetSupervisor(); s != nil {
		return s.Wanted(event)
	}
	return false
}

// HasPipeline reports whether a running plugin holds pipeline:<name>.
func (s *Supervisor) HasPipeline(name string) bool {
	for _, e := range s.mgr.List() {
		if wantsProcess(e) && holdsPipeline(e, name) {
			return true
		}
	}
	return false
}

// HasPipeline reports whether any plugin would transform the named pipeline.
func HasPipeline(name string) bool {
	if s := GetSupervisor(); s != nil {
		return s.HasPipeline(name)
	}
	return false
}
