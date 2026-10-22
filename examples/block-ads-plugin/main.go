// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"log"
	"net/http"
)

const adsDomain = "geosite:category-ads-all"

func main() {
	mux := http.NewServeMux()
	mux.Handle("POST /_lucx/pipeline/xray.config", PipelineHandler(adsRulePatch))
	log.Print("block-ads plugin ready")
	if err := Serve(mux); err != nil {
		log.Fatal(err)
	}
}

type xrayDoc struct {
	Routing *struct {
		Rules []struct {
			Domain []string `json:"domain"`
		} `json:"rules"`
	} `json:"routing"`
	Outbounds []struct {
		Tag string `json:"tag"`
	} `json:"outbounds"`
}

// adsRulePatch is called on every config generation, so it must give the same
// answer for a document that already has its change: it then returns nothing.
func adsRulePatch(raw json.RawMessage) ([]PatchOp, error) {
	var doc xrayDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	hasBlocked := false
	for _, o := range doc.Outbounds {
		hasBlocked = hasBlocked || o.Tag == "blocked"
	}
	if !hasBlocked {
		return nil, nil // nothing to route ads to
	}
	rule := map[string]any{"type": "field", "domain": []string{adsDomain}, "outboundTag": "blocked"}
	switch {
	case doc.Routing == nil:
		return []PatchOp{{Op: "add", Path: "/routing", Value: map[string]any{"rules": []any{rule}}}}, nil
	case doc.Routing.Rules == nil:
		return []PatchOp{{Op: "add", Path: "/routing/rules", Value: []any{rule}}}, nil
	}
	for _, r := range doc.Routing.Rules {
		for _, d := range r.Domain {
			if d == adsDomain {
				return nil, nil
			}
		}
	}
	// First position: rules are matched in order, so ads must not reach a catch-all above.
	return []PatchOp{{Op: "add", Path: "/routing/rules/0", Value: rule}}, nil
}
