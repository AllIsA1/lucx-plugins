// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package job

import (
	"context"
	"sync"

	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// NodePluginsJob refreshes what the master knows about plugins on its nodes.
type NodePluginsJob struct {
	svc     *service.NodePluginsService
	running sync.Mutex
}

// NewNodePluginsJob creates the polling job.
func NewNodePluginsJob() *NodePluginsJob {
	return &NodePluginsJob{svc: service.NewNodePluginsService()}
}

// Run polls once; a run still in progress makes the next tick a no-op.
func (j *NodePluginsJob) Run() {
	if !j.running.TryLock() {
		return
	}
	defer j.running.Unlock()
	j.svc.Poll(context.Background())
}
