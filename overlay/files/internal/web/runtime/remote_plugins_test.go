// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package runtime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPluginRefusalsKeepTheNodesOwnWords(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantStatus int
		wantMsg    string
	}{
		{"locked out", http.StatusForbidden, `{"success":false,"msg":"plugin management by the master is disabled on this node","obj":null}`, http.StatusForbidden, "plugin management by the master is disabled on this node"},
		{"not json", http.StatusForbidden, `Forbidden`, http.StatusForbidden, "Forbidden"},
		{"failed command", http.StatusInternalServerError, `{"success":false,"msg":"boom"}`, http.StatusInternalServerError, "boom"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			r := NewRemote(nodeForPlainServer(t, srv, "verify", "tok"), nil)

			_, err := r.PluginsList(context.Background())
			var refused *RemotePluginError
			if !errors.As(err, &refused) {
				t.Fatalf("err = %v, want a *RemotePluginError", err)
			}
			if refused.Status != tc.wantStatus || refused.Error() != tc.wantMsg {
				t.Errorf("refusal = %d %q, want %d %q", refused.Status, refused.Error(), tc.wantStatus, tc.wantMsg)
			}
		})
	}

	t.Run("an unreachable node is not a refusal", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		r := NewRemote(nodeForPlainServer(t, srv, "verify", "tok"), nil)
		srv.Close()
		_, err := r.PluginsList(context.Background())
		var refused *RemotePluginError
		if err == nil || errors.As(err, &refused) {
			t.Errorf("err = %v, want a plain transport error", err)
		}
	})
}
