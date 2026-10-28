// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package sub

import (
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// A WARP inbound has no link of the panel's own (the Better WARP plugin writes its lines into the answer), but a client
// on it is a subscriber: the answer is an empty subscription, not a 404 the plugin could never add to.
func TestGetSubs_WarpOnlyClientGetsAnEmptySubscriptionNotNotFound(t *testing.T) {
	initSubDB(t)
	const email, subID = "alice@warp", "sub-warp-only"
	seedTunnelSubInbound(t, model.Warp, "warp-in", subID, email, `{"endpoints":["162.159.192.1:2408"],"clients":[]}`, 0)

	links, emails, _, _, err := NewSubService("").GetSubs(subID, "sub.example.com")
	if err != nil {
		t.Fatalf("GetSubs: %v", err)
	}
	if links == nil {
		t.Fatal("links is nil: the subscription controller answers 404 for it")
	}
	if len(links) != 0 {
		t.Errorf("the panel has no link of its own for WARP, got %q", links)
	}
	if len(emails) != 1 || emails[0] != email {
		t.Errorf("emails = %v", emails)
	}
}

// A subscriber with a WARP inbound next to others keeps the others' links and gets no blank line for WARP.
func TestGetSubs_WarpNextToOtherInboundsAddsNoBlankLine(t *testing.T) {
	initSubDB(t)
	const subID = "sub-warp-mixed"
	seedSubInbound(t, subID, "vless-in", 4431, 1, `{"network":"tcp","security":"none","tcpSettings":{}}`)
	seedTunnelSubInbound(t, model.Warp, "warp-in2", subID, "carol@warp", `{"clients":[]}`, 0)

	links, _, _, _, err := NewSubService("").GetSubs(subID, "sub.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0] == "" {
		t.Errorf("links = %q: want the VLESS link only", links)
	}
}
