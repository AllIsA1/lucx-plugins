// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

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
