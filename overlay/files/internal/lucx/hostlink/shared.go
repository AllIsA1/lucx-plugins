// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostlink

import (
	"context"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/eventbus"
)

// The panel has one plugin host, so it has one link. The functions below are what the rest of the
// panel calls; each is a no-op or "nothing" while the host is not running.
var (
	sharedMu sync.Mutex
	shared   *Link
)

// Shared is the panel's link, created on first use from DefaultConfig.
func Shared() *Link {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if shared == nil {
		shared = New(DefaultConfig())
	}
	return shared
}

// SetShared replaces the panel's link (tests).
func SetShared(l *Link) {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	shared = l
}

// Start begins talking to the host. It never fails and never blocks.
func Start() { Shared().Start() }

// Stop ends it.
func Stop() { Shared().Stop() }

// RegisterAdmin mounts the plugin admin routes on the /panel/api/plugins group.
func RegisterAdmin(g *gin.RouterGroup) { Shared().RegisterAdmin(g) }

// RegisterFrame mounts the plugin page route on the group that carries the panel's base path.
func RegisterFrame(g *gin.RouterGroup) { Shared().RegisterFrame(g) }

// Wanted reports whether some enabled plugin subscribed to the event.
func Wanted(event string) bool { return Shared().Wanted(event) }

// HasPipeline reports whether some enabled plugin holds the pipeline.
func HasPipeline(name string) bool { return Shared().HasPipeline(name) }

// HandleEvent is the event bus subscriber.
func HandleEvent(ev eventbus.Event) { Shared().HandleEvent(ev) }

// Transform runs doc through a pipeline; any failure returns doc unchanged.
func Transform(ctx context.Context, name string, doc []byte) []byte {
	return Shared().Transform(ctx, name, doc)
}
