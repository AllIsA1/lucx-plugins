// Copyright (c) 2026 LucX-UI Project.
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func patchFor(t *testing.T, doc string) []PatchOp {
	t.Helper()
	ops, err := adsRulePatch(json.RawMessage(doc))
	if err != nil {
		t.Fatal(err)
	}
	return ops
}

func TestAddsTheRuleFirstWhenAnOutboundCanTakeIt(t *testing.T) {
	ops := patchFor(t, `{"routing":{"rules":[{"outboundTag":"direct"}]},"outbounds":[{"tag":"direct"},{"tag":"blocked"}]}`)
	if len(ops) != 1 || ops[0].Op != "add" || ops[0].Path != "/routing/rules/0" {
		t.Fatalf("ops = %+v, want one insert at the first position", ops)
	}
}

func TestCreatesRoutingWhenTheTemplateHasNone(t *testing.T) {
	ops := patchFor(t, `{"outbounds":[{"tag":"blocked"}]}`)
	if len(ops) != 1 || ops[0].Path != "/routing" {
		t.Fatalf("ops = %+v", ops)
	}
	ops = patchFor(t, `{"routing":{},"outbounds":[{"tag":"blocked"}]}`)
	if len(ops) != 1 || ops[0].Path != "/routing/rules" {
		t.Fatalf("ops = %+v", ops)
	}
}

func TestIsIdempotentAndHarmlessWithoutABlockedOutbound(t *testing.T) {
	if ops := patchFor(t, `{"routing":{"rules":[{"domain":["geosite:category-ads-all"],"outboundTag":"blocked"}]},"outbounds":[{"tag":"blocked"}]}`); len(ops) != 0 {
		t.Errorf("the panel asks on every generation; a document that already has the rule needs no change: %+v", ops)
	}
	if ops := patchFor(t, `{"routing":{"rules":[]},"outbounds":[{"tag":"direct"}]}`); len(ops) != 0 {
		t.Errorf("without a blocked outbound there is nowhere to send ads: %+v", ops)
	}
}

func TestServesThePipelineEndpoint(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("POST /_lucx/pipeline/xray.config", PipelineHandler(adsRulePatch))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/_lucx/pipeline/xray.config",
		strings.NewReader(`{"routing":{"rules":[]},"outbounds":[{"tag":"blocked"}]}`)))
	var reply struct{ Patch []PatchOp }
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &reply) != nil || len(reply.Patch) != 1 {
		t.Fatalf("reply = %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/_lucx/pipeline/xray.config", strings.NewReader(`{"outbounds":[]}`)))
	if rec.Code != http.StatusNoContent {
		t.Errorf("no change must answer 204, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/_lucx/pipeline/xray.config", strings.NewReader(`{broken`)))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("an unreadable document must be an error, got %d", rec.Code)
	}
}
