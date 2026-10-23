// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostlink

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/eventbus"
)

// wireEvent is the event as the host expects it.
type wireEvent struct {
	Type      string    `json:"type"`
	Source    string    `json:"source"`
	Data      any       `json:"data"`
	Timestamp time.Time `json:"timestamp"`
}

// HandleEvent queues a panel event for the host when some plugin subscribed to it. It never blocks
// the bus: a full queue drops the event, as a slow subscriber always did.
func (l *Link) HandleEvent(ev eventbus.Event) {
	if !l.Wanted(string(ev.Type)) {
		return
	}
	body, err := json.Marshal(wireEvent{Type: string(ev.Type), Source: ev.Source, Data: ev.Data, Timestamp: ev.Timestamp})
	if err != nil {
		return
	}
	select {
	case l.events <- body:
	default:
		warnRateLimited(&l.warned, "plugins: event queue is full, dropping %s", ev.Type)
	}
}

func (l *Link) sendLoop(ctx context.Context) {
	defer l.done.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case body := <-l.events:
			l.send(ctx, body)
		}
	}
}

func (l *Link) send(ctx context.Context, body []byte) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://host/internal/events", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := l.call.Do(req)
	if err != nil {
		warnRateLimited(&l.warned, "plugins: cannot deliver events to the plugin system: %v", err)
		return
	}
	_ = resp.Body.Close()
}
