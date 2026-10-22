// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import "time"

// EventType names a panel event; the constants are the events plugins can subscribe to.
type EventType string

const (
	EventOutboundDown           EventType = "outbound.down"
	EventOutboundUp             EventType = "outbound.up"
	EventXrayCrash              EventType = "xray.crash"
	EventNodeDown               EventType = "node.down"
	EventNodeUp                 EventType = "node.up"
	EventCPUHigh                EventType = "cpu.high"
	EventMemoryHigh             EventType = "memory.high"
	EventLoginAttempt           EventType = "login.attempt"
	EventClientCreated          EventType = "client.created"
	EventClientUpdated          EventType = "client.updated"
	EventClientDeleted          EventType = "client.deleted"
	EventClientTrafficExhausted EventType = "client.traffic.exhausted"
	EventClientExpired          EventType = "client.expired"
	EventInboundCreated         EventType = "inbound.created"
	EventInboundUpdated         EventType = "inbound.updated"
	EventInboundDeleted         EventType = "inbound.deleted"
)

// Event is a panel event as the panel integration forwards it to the host.
type Event struct {
	Type      EventType `json:"type"`
	Source    string    `json:"source"`
	Data      any       `json:"data"`
	Timestamp time.Time `json:"timestamp"`
}
