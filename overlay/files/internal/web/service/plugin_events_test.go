// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/eventbus"
	"github.com/mhsanaei/3x-ui/v3/internal/lucx/plugins"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"

	"gorm.io/gorm"
)

type eventLog struct {
	mu     sync.Mutex
	events []eventbus.Event
}

func (l *eventLog) add(e eventbus.Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
}

func (l *eventLog) types() []eventbus.EventType {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]eventbus.EventType, len(l.events))
	for i, e := range l.events {
		out[i] = e.Type
	}
	return out
}

func (l *eventLog) wait(t *testing.T, n int) []eventbus.Event {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		l.mu.Lock()
		if len(l.events) >= n {
			out := append([]eventbus.Event(nil), l.events...)
			l.mu.Unlock()
			return out
		}
		l.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("got events %v, want %d", l.types(), n)
	return nil
}

// busWithSubscriber starts the plugin manager with one enabled plugin that
// subscribed to events, and returns what the panel bus carried.
func busWithSubscriber(t *testing.T, events ...string) *eventLog {
	t.Helper()
	root := t.TempDir()
	cfg := plugins.Config{
		CodeDir:      filepath.Join(root, "plugins"),
		DataDir:      filepath.Join(root, "data"),
		RegistryPath: filepath.Join(root, "plugins.json"),
		Runner:       plugins.NewUnavailableRunner("tests"),
	}
	perms := []string{"api:clients:read"}
	for _, e := range events {
		perms = append(perms, "events:"+e)
	}
	raw, err := json.Marshal(map[string]any{"schema": 1, "plugins": []plugins.Entry{{
		ID: "watcher", Name: "Watcher", Version: "1.0.0", Type: plugins.TypeProcess, Scope: plugins.ScopeMaster,
		Commit: "0123456789abcdef0123456789abcdef01234567", Enabled: true, Permissions: perms,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.RegistryPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XUI_PLUGINS_DISABLED", "")
	plugins.Start(cfg)
	t.Cleanup(plugins.Stop)

	bus := eventbus.New(0)
	log := &eventLog{}
	bus.Subscribe("test", log.add)
	SetEventBus(bus)
	t.Cleanup(func() { SetEventBus(nil); bus.Stop() })
	return log
}

func TestClientLifecycleEventsReachTheBus(t *testing.T) {
	setupBulkDB(t)
	log := busWithSubscriber(t, "client.created", "client.updated", "client.deleted")
	svc, inboundSvc := &ClientService{}, &InboundService{}
	ib := mkInbound(t, 30601, model.VLESS, `{"clients":[]}`)

	if _, err := svc.Create(inboundSvc, &ClientCreatePayload{
		Client:     model.Client{Email: "alice@example.com", ID: "11111111-1111-1111-1111-111111111111", Enable: true},
		InboundIds: []int{ib.Id},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	rec, err := svc.GetRecordByEmail(nil, "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	toggled := *rec.ToClient()
	toggled.Enable = false
	if _, err := svc.Update(inboundSvc, rec.Id, toggled, 0); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, err := svc.Delete(inboundSvc, rec.Id, false); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got := log.wait(t, 3)
	want := []eventbus.EventType{eventbus.EventClientCreated, eventbus.EventClientUpdated, eventbus.EventClientDeleted}
	for i, e := range got {
		if e.Type != want[i] || e.Source != "alice@example.com" {
			t.Errorf("event %d = %s from %q, want %s from alice@example.com", i, e.Type, e.Source, want[i])
		}
		data, ok := e.Data.(eventbus.ClientEventData)
		if !ok || data.Email != "alice@example.com" {
			t.Errorf("event %d data = %#v", i, e.Data)
		}
	}
	if created := got[0].Data.(eventbus.ClientEventData); len(created.InboundIDs) != 1 || created.InboundIDs[0] != ib.Id {
		t.Errorf("created event inbound ids = %v", created.InboundIDs)
	}
}

func TestNothingIsPublishedWhenNoPluginSubscribed(t *testing.T) {
	setupBulkDB(t)
	log := busWithSubscriber(t, "client.deleted") // subscribed to deletions only
	svc, inboundSvc := &ClientService{}, &InboundService{}
	ib := mkInbound(t, 30602, model.VLESS, `{"clients":[]}`)

	if _, err := svc.Create(inboundSvc, &ClientCreatePayload{
		Client:     model.Client{Email: "quiet@example.com", ID: "22222222-2222-2222-2222-222222222222", Enable: true},
		InboundIds: []int{ib.Id},
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if got := log.types(); len(got) != 0 {
		t.Errorf("events nobody subscribed to were published: %v", got)
	}
}

func TestFailedOperationsPublishNothing(t *testing.T) {
	setupBulkDB(t)
	log := busWithSubscriber(t, "client.deleted", "client.created")
	svc, inboundSvc := &ClientService{}, &InboundService{}
	db := database.GetDB()
	broken := mkInbound(t, 30603, model.VLESS, `{"clients": [ THIS IS NOT JSON`)
	rec := &model.ClientRecord{Email: "broken@example.com", Enable: true, UUID: "33333333-3333-3333-3333-333333333333"}
	if err := db.Create(rec).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ClientInbound{ClientId: rec.Id, InboundId: broken.Id}).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { withdrawClientTombstones("broken@example.com") })

	if _, err := svc.Delete(inboundSvc, rec.Id, false); err == nil {
		t.Fatal("setup: the delete was expected to fail")
	}
	if _, err := svc.Create(inboundSvc, &ClientCreatePayload{Client: model.Client{Email: "  "}}); err == nil {
		t.Fatal("setup: the create was expected to fail")
	}
	time.Sleep(100 * time.Millisecond)
	if got := log.types(); len(got) != 0 {
		t.Errorf("a failed operation must not announce success: %v", got)
	}
}

func TestInboundLifecycleEventsReachTheBus(t *testing.T) {
	setupBulkDB(t)
	log := busWithSubscriber(t, "inbound.created", "inbound.updated", "inbound.deleted")
	inboundSvc := &InboundService{}

	created, _, err := inboundSvc.AddInbound(&model.Inbound{Tag: "watched-in", Port: 30604, Protocol: model.VLESS, Enable: true, Remark: "watched", Settings: `{"clients":[]}`})
	if err != nil {
		t.Fatalf("AddInbound: %v", err)
	}
	created.Remark = "renamed"
	if _, _, err := inboundSvc.UpdateInbound(created); err != nil {
		t.Fatalf("UpdateInbound: %v", err)
	}
	if _, err := inboundSvc.DelInbound(created.Id); err != nil {
		t.Fatalf("DelInbound: %v", err)
	}

	got := log.wait(t, 3)
	want := []eventbus.EventType{eventbus.EventInboundCreated, eventbus.EventInboundUpdated, eventbus.EventInboundDeleted}
	for i, e := range got {
		data, ok := e.Data.(eventbus.InboundEventData)
		if e.Type != want[i] || !ok || data.ID != created.Id || data.Tag != "watched-in" || data.Protocol != "vless" {
			t.Errorf("event %d = %s %#v", i, e.Type, e.Data)
		}
	}
}

func TestDepletedClientsAreReportedWithTheRightReason(t *testing.T) {
	setupBulkDB(t)
	log := busWithSubscriber(t, "client.traffic.exhausted", "client.expired")
	db := database.GetDB()
	ib := mkInbound(t, 30605, model.VLESS, `{"clients":[{"email":"spent@example.com","id":"44444444-4444-4444-4444-444444444444","enable":true},{"email":"late@example.com","id":"55555555-5555-5555-5555-555555555555","enable":true}]}`)

	seed := func(email string, traffic xray.ClientTraffic) {
		rec := &model.ClientRecord{Email: email, Enable: true, UUID: "66666666-6666-6666-6666-666666666666"}
		if err := db.Create(rec).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.ClientInbound{ClientId: rec.Id, InboundId: ib.Id}).Error; err != nil {
			t.Fatal(err)
		}
		traffic.Email, traffic.InboundId, traffic.Enable = email, ib.Id, true
		if err := db.Create(&traffic).Error; err != nil {
			t.Fatal(err)
		}
	}
	seed("spent@example.com", xray.ClientTraffic{Total: 100, Up: 70, Down: 60})
	seed("late@example.com", xray.ClientTraffic{ExpiryTime: time.Now().Add(-time.Hour).UnixMilli()})

	if err := db.Transaction(func(tx *gorm.DB) error {
		_, _, _, err := (&InboundService{}).disableInvalidClients(tx, &trafficMutationBatch{})
		return err
	}); err != nil {
		t.Fatalf("disableInvalidClients: %v", err)
	}

	got := map[string]eventbus.EventType{}
	for _, e := range log.wait(t, 2) {
		got[e.Source] = e.Type
	}
	if got["spent@example.com"] != eventbus.EventClientTrafficExhausted || got["late@example.com"] != eventbus.EventClientExpired {
		t.Errorf("events = %v", got)
	}
}
