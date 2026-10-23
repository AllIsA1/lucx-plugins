// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package service

import (
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/eventbus"
	"github.com/mhsanaei/3x-ui/v3/internal/lucx/hostlink"
	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

// emitPluginEvent publishes a domain event, but only when some enabled plugin
// (reported by the plugin host) subscribed to it: everything else would just fill the bus.
func emitPluginEvent(t eventbus.EventType, source string, data any) {
	if eventBus == nil || !hostlink.Wanted(string(t)) {
		return
	}
	eventBus.Publish(eventbus.Event{Type: t, Source: source, Data: data})
}

func pluginWants(t eventbus.EventType) bool {
	return eventBus != nil && hostlink.Wanted(string(t))
}

// Create adds a client and tells plugins about it.
func (s *ClientService) Create(inboundSvc *InboundService, payload *ClientCreatePayload) (bool, error) {
	needRestart, err := s.createUnobserved(inboundSvc, payload)
	if err == nil && payload != nil {
		emitPluginEvent(eventbus.EventClientCreated, payload.Client.Email,
			eventbus.ClientEventData{Email: payload.Client.Email, InboundIDs: payload.InboundIds})
	}
	return needRestart, err
}

// Update edits a client and tells plugins about it.
func (s *ClientService) Update(inboundSvc *InboundService, id int, updated model.Client, limitHwid int, inboundFilter ...int) (bool, error) {
	needRestart, err := s.updateUnobserved(inboundSvc, id, updated, limitHwid, inboundFilter...)
	if err == nil {
		emitPluginEvent(eventbus.EventClientUpdated, updated.Email,
			eventbus.ClientEventData{Email: updated.Email, ClientID: id})
	}
	return needRestart, err
}

// Delete removes a client and tells plugins about it.
func (s *ClientService) Delete(inboundSvc *InboundService, id int, keepTraffic bool) (bool, error) {
	email := ""
	if pluginWants(eventbus.EventClientDeleted) {
		if rec, err := s.GetByID(id); err == nil {
			email = rec.Email
		}
	}
	needRestart, err := s.deleteUnobserved(inboundSvc, id, keepTraffic)
	if err == nil && email != "" {
		emitPluginEvent(eventbus.EventClientDeleted, email, eventbus.ClientEventData{Email: email, ClientID: id})
	}
	return needRestart, err
}

func inboundEventData(ib *model.Inbound) eventbus.InboundEventData {
	return eventbus.InboundEventData{ID: ib.Id, Tag: ib.Tag, Protocol: string(ib.Protocol), Remark: ib.Remark, Port: ib.Port}
}

// AddInbound creates an inbound and tells plugins about it.
func (s *InboundService) AddInbound(inbound *model.Inbound) (*model.Inbound, bool, error) {
	created, needRestart, err := s.addInboundUnobserved(inbound)
	if err == nil && created != nil {
		emitPluginEvent(eventbus.EventInboundCreated, created.Tag, inboundEventData(created))
	}
	return created, needRestart, err
}

// UpdateInbound edits an inbound and tells plugins about it.
func (s *InboundService) UpdateInbound(inbound *model.Inbound) (*model.Inbound, bool, error) {
	updated, needRestart, err := s.updateInboundUnobserved(inbound)
	if err == nil && updated != nil {
		emitPluginEvent(eventbus.EventInboundUpdated, updated.Tag, inboundEventData(updated))
	}
	return updated, needRestart, err
}

// DelInbound removes an inbound and tells plugins about it.
func (s *InboundService) DelInbound(id int) (bool, error) {
	var snapshot *model.Inbound
	if pluginWants(eventbus.EventInboundDeleted) {
		snapshot, _ = s.GetInbound(id)
	}
	needRestart, err := s.delInboundUnobserved(id)
	if err == nil && snapshot != nil {
		emitPluginEvent(eventbus.EventInboundDeleted, snapshot.Tag, inboundEventData(snapshot))
	}
	return needRestart, err
}

// publishDepletedClients reports clients the traffic job just switched off,
// telling a spent quota from an expired subscription.
func publishDepletedClients(rows []xray.ClientTraffic) {
	for i := range rows {
		r := rows[i]
		if r.Email == "" {
			continue
		}
		typ := eventbus.EventClientExpired
		if r.Total > 0 && r.Up+r.Down >= r.Total {
			typ = eventbus.EventClientTrafficExhausted
		}
		emitPluginEvent(typ, r.Email, eventbus.ClientEventData{Email: r.Email})
	}
}
