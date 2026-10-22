// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"
)

const (
	defaultPipelineTimeout = 2 * time.Second
	pipelineCacheTTL       = 30 * time.Second
	maxPipelineReply       = 1 << 20
	pipelinePath           = reservedPrefix + "/pipeline/"
)

// PipelineResult reports what one plugin did to a document.
type PipelineResult struct {
	Plugin  string
	Applied int
	Err     error
}

type cachedPatch struct {
	ops     []PatchOp
	expires time.Time
}

// Transform runs the document through every running plugin that holds
// pipeline:<name>, in plugin id order, each seeing the previous result. It is
// fail-open: a plugin that errors, times out, returns a bad patch or one the
// validator refuses is skipped, and the document it was given stays as it was.
func (s *Supervisor) Transform(ctx context.Context, name string, doc []byte, validate func(before, after []byte) error) ([]byte, []PipelineResult) {
	var targets []Entry
	for _, e := range s.mgr.List() {
		if !wantsProcess(e) || !holdsPipeline(e, name) {
			continue
		}
		if rt, _ := s.Runtime(e.ID); rt.State == StateRunning {
			targets = append(targets, e)
		}
	}
	slices.SortFunc(targets, func(a, b Entry) int { return strings.Compare(a.ID, b.ID) })

	current := doc
	results := make([]PipelineResult, 0, len(targets))
	for _, e := range targets {
		next, applied, err := s.runPipelinePlugin(ctx, e, name, current, validate)
		if err != nil {
			logger.Warningf("plugins: pipeline %s of %s skipped: %v", name, e.ID, err)
			s.bump(e.ID, func(st *pluginStats) {
				st.pipelineErrors++
				st.lastError = "pipeline " + name + ": " + err.Error()
			})
		} else {
			current = next
		}
		results = append(results, PipelineResult{Plugin: e.ID, Applied: applied, Err: err})
	}
	return current, results
}

func (s *Supervisor) runPipelinePlugin(ctx context.Context, e Entry, name string, doc []byte, validate func(before, after []byte) error) ([]byte, int, error) {
	ops, err := s.patchFor(ctx, e, name, doc)
	if err != nil {
		return nil, 0, err
	}
	if len(ops) == 0 {
		return doc, 0, nil
	}
	patched, err := ApplyPatch(doc, ops)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid patch: %w", err)
	}
	if validate != nil {
		if err := validate(doc, patched); err != nil {
			return nil, 0, fmt.Errorf("the panel refused the result: %w", err)
		}
	}
	return patched, len(ops), nil
}

func (s *Supervisor) patchFor(ctx context.Context, e Entry, name string, doc []byte) ([]PatchOp, error) {
	sum := sha256.Sum256(doc)
	key := fmt.Sprintf("%s|%s|%s|%x", e.ID, Fingerprint(e), name, sum)
	now := time.Now()
	s.cacheMu.Lock()
	if c, ok := s.patchCache[key]; ok && now.Before(c.expires) {
		s.cacheMu.Unlock()
		return c.ops, nil
	}
	s.cacheMu.Unlock()

	ops, err := s.askPlugin(ctx, e.ID, name, doc)
	if err != nil {
		return nil, err
	}
	s.cacheMu.Lock()
	for k, c := range s.patchCache {
		if now.After(c.expires) {
			delete(s.patchCache, k)
		}
	}
	s.patchCache[key] = cachedPatch{ops: ops, expires: now.Add(pipelineCacheTTL)}
	s.cacheMu.Unlock()
	return ops, nil
}

func (s *Supervisor) askPlugin(ctx context.Context, id, name string, doc []byte) ([]PatchOp, error) {
	ctx, cancel := context.WithTimeout(ctx, s.opt.PipelineTimeout)
	defer cancel()
	socket, _ := s.runner.Paths(id)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://plugin"+pipelinePath+name, bytes.NewReader(doc))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-LucX-Pipeline", name)
	resp, err := newSocketClient(socket, s.opt.PipelineTimeout).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil, nil
	case http.StatusOK:
	default:
		return nil, fmt.Errorf("plugin answered HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxPipelineReply+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxPipelineReply {
		return nil, fmt.Errorf("reply is larger than %d bytes", maxPipelineReply)
	}
	var reply struct {
		Patch []PatchOp `json:"patch"`
	}
	if err := json.Unmarshal(raw, &reply); err != nil {
		return nil, fmt.Errorf("reply is not a patch document: %w", err)
	}
	if len(reply.Patch) > MaxPatchOps {
		return nil, fmt.Errorf("patch has %d operations, the limit is %d", len(reply.Patch), MaxPatchOps)
	}
	return reply.Patch, nil
}

// TransformPipeline runs the running supervisor's pipeline; without one the
// document comes back untouched.
func TransformPipeline(ctx context.Context, name string, doc []byte, validate func(before, after []byte) error) ([]byte, []PipelineResult) {
	if s := GetSupervisor(); s != nil {
		return s.Transform(ctx, name, doc, validate)
	}
	return doc, nil
}
