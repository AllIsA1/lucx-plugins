// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

// ClientEventData is the payload the panel sends with client events; the host treats it as opaque,
// the tests use this copy to build realistic events.
type ClientEventData struct {
	Email      string `json:"email"`
	ClientID   int    `json:"clientId,omitempty"`
	InboundIDs []int  `json:"inboundIds,omitempty"`
}
