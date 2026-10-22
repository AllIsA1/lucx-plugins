package eventbus

import "time"

// EventType identifies the kind of event flowing through the bus.
type EventType string

const (
	// Outbound health (observatory-driven)
	EventOutboundDown EventType = "outbound.down"
	EventOutboundUp   EventType = "outbound.up"

	// Xray core (local)
	EventXrayCrash EventType = "xray.crash"

	// Node health (heartbeat-driven)
	EventNodeDown EventType = "node.down"
	EventNodeUp   EventType = "node.up"

	// System health
	EventCPUHigh    EventType = "cpu.high"
	EventMemoryHigh EventType = "memory.high"

	// Security
	EventLoginAttempt EventType = "login.attempt"

	// LUCX-HOOK: domain events delivered to plugins (see internal/lucx/plugins).
	EventClientCreated          EventType = "client.created"
	EventClientUpdated          EventType = "client.updated"
	EventClientDeleted          EventType = "client.deleted"
	EventClientTrafficExhausted EventType = "client.traffic.exhausted"
	EventClientExpired          EventType = "client.expired"
	EventInboundCreated         EventType = "inbound.created"
	EventInboundUpdated         EventType = "inbound.updated"
	EventInboundDeleted         EventType = "inbound.deleted"
	// END LUCX-HOOK
)

// Event is the unit of information flowing through the bus.
type Event struct {
	Type      EventType
	Source    string    // outbound tag, node name, client email, IP, etc.
	Data      any       // event-specific payload, may be nil
	Timestamp time.Time // when the event was detected
}

// OutboundHealthData carries observatory details for outbound events.
type OutboundHealthData struct {
	Delay int64  // last measured delay in ms, 0 if unknown
	Error string // last error if probe failed, empty if up
}

// NodeHealthData carries heartbeat details for node events.
type NodeHealthData struct {
	NodeId    int
	LatencyMs int
	CpuPct    float64
	MemPct    float64
	XrayState string // "running", "stopped", etc.
	XrayError string
}

// LoginEventData carries login attempt details.
type LoginEventData struct {
	Username string
	IP       string
	Time     string
	Status   string // "success" or "fail"
	Reason   string
}

// SystemMetricData carries raw system metric values for threshold-based events.
type SystemMetricData struct {
	Percent   float64 // current usage percentage
	Threshold int     // configured threshold
}

// LUCX-HOOK: payloads of the domain events above. They carry identifiers
// only, never keys, passwords or UUIDs.

// ClientEventData describes a client lifecycle event.
type ClientEventData struct {
	Email      string `json:"email"`
	ClientID   int    `json:"clientId,omitempty"`
	InboundIDs []int  `json:"inboundIds,omitempty"`
}

// InboundEventData describes an inbound lifecycle event.
type InboundEventData struct {
	ID       int    `json:"id"`
	Tag      string `json:"tag,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	Remark   string `json:"remark,omitempty"`
	Port     int    `json:"port,omitempty"`
}

// END LUCX-HOOK
