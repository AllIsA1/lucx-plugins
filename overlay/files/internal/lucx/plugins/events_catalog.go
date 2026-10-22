// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"slices"
	"strings"
)

// PipelineXrayConfig lets a plugin patch the generated Xray configuration.
const PipelineXrayConfig = "xray.config"

var knownEvents = []string{
	"client.created", "client.updated", "client.deleted",
	"client.traffic.exhausted", "client.expired",
	"inbound.created", "inbound.updated", "inbound.deleted",
	"xray.crash", "node.down", "node.up", "cpu.high", "memory.high",
	"outbound.down", "outbound.up", "login.attempt",
}

var knownPipelines = []string{PipelineXrayConfig}

// KnownEvents lists every event a plugin can subscribe to.
func KnownEvents() []string { return slices.Clone(knownEvents) }

// IsKnownEvent reports whether name is a deliverable event type.
func IsKnownEvent(name string) bool { return slices.Contains(knownEvents, name) }

// IsKnownPipeline reports whether name is a pipeline the panel runs.
func IsKnownPipeline(name string) bool { return slices.Contains(knownPipelines, name) }

func subscribed(e Entry, event string) bool {
	return slices.Contains(e.Permissions, "events:"+event)
}

func holdsPipeline(e Entry, name string) bool {
	return slices.Contains(e.Permissions, "pipeline:"+name) && !strings.Contains(name, " ")
}
