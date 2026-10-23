// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostlink

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"
)

// PipelineXrayConfig is the pipeline plugins use to patch the generated Xray configuration.
const PipelineXrayConfig = "xray.config"

// pipelineBudget bounds the whole round trip; the host also bounds each plugin it asks.
var pipelineBudget = 10 * time.Second

// Transform sends doc through the pipeline and returns what came back. Any failure returns doc
// unchanged (fail-open): the caller still checks the result before using it.
func (l *Link) Transform(ctx context.Context, name string, doc []byte) []byte {
	if !l.HasPipeline(name) {
		return doc
	}
	ctx, cancel := context.WithTimeout(ctx, pipelineBudget)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://host/internal/pipeline/"+name, bytes.NewReader(doc))
	if err != nil {
		return doc
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Transport: l.call.Transport, Timeout: pipelineBudget}
	resp, err := client.Do(req)
	if err != nil {
		warnRateLimited(&l.warned, "plugins: pipeline %s skipped: %v", name, err)
		return doc
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return doc
	}
	out, err := io.ReadAll(io.LimitReader(resp.Body, maxPipelineBody+1))
	if err != nil || len(out) == 0 || len(out) > maxPipelineBody {
		return doc
	}
	return out
}
