// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/eventbus"
)

func TestEventCatalogCoversEveryBusEvent(t *testing.T) {
	for _, typ := range []eventbus.EventType{
		eventbus.EventOutboundDown, eventbus.EventOutboundUp, eventbus.EventXrayCrash,
		eventbus.EventNodeDown, eventbus.EventNodeUp, eventbus.EventCPUHigh,
		eventbus.EventMemoryHigh, eventbus.EventLoginAttempt,
		eventbus.EventClientCreated, eventbus.EventClientUpdated, eventbus.EventClientDeleted,
		eventbus.EventClientTrafficExhausted, eventbus.EventClientExpired,
		eventbus.EventInboundCreated, eventbus.EventInboundUpdated, eventbus.EventInboundDeleted,
	} {
		if !IsKnownEvent(string(typ)) {
			t.Errorf("bus event %q is not in the plugin event catalog, so no plugin can subscribe to it", typ)
		}
		if err := ValidatePermission("events:" + string(typ)); err != nil {
			t.Errorf("events:%s is rejected by the permission grammar: %v", typ, err)
		}
	}
}

func TestManifestsCannotSubscribeToEventsOrPipelinesThatDoNotExist(t *testing.T) {
	for _, p := range []string{"events:client.craeted", "events:client", "pipeline:xray.confg", "pipeline:sub.link"} {
		if err := ValidatePermission(p); err == nil {
			t.Errorf("%s is accepted although nothing will ever deliver it", p)
		}
	}
	for _, p := range []string{"events:client.created", "events:inbound.deleted", "pipeline:xray.config"} {
		if err := ValidatePermission(p); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}

func TestSubscribedMatchesExactEventPermissions(t *testing.T) {
	e := Entry{Permissions: []string{"events:client.created", "api:clients:read", "pipeline:xray.config"}}
	if !subscribed(e, "client.created") || subscribed(e, "client.deleted") || subscribed(e, "xray.config") {
		t.Error("subscribed must match only the exact events: permissions it holds")
	}
	if !holdsPipeline(e, "xray.config") || holdsPipeline(e, "client.created") {
		t.Error("holdsPipeline must match only pipeline: permissions")
	}
	if strings.Contains(strings.Join(KnownEvents(), ","), "pipeline") {
		t.Error("pipelines are not events")
	}
}
