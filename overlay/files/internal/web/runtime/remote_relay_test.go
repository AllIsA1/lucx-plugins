// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package runtime

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func relayTo(t *testing.T, handler http.HandlerFunc, method, path string, body []byte) (int, string, error) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	r := NewRemote(nodeForPlainServer(t, srv, "verify", "tok"), nil)
	status, out, err := RelayPlugin(context.Background(), r, method, path, body)
	return status, string(out), err
}

func TestRelayCarriesTheCallAndTheNodesAnswerThrough(t *testing.T) {
	var gotMethod, gotPath, gotBody, gotAuth string
	status, body, err := relayTo(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.RequestURI(), r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		_, _ = w.Write([]byte(`{"success":true,"msg":"","obj":{"ok":1}}`))
	}, http.MethodPost, "/panel/api/plugins/remote/install", []byte(`{"url":"https://github.com/a/b"}`))
	if err != nil || status != http.StatusOK || !strings.Contains(body, `"ok":1`) {
		t.Fatalf("relay = %d %q, %v", status, body, err)
	}
	if gotMethod != http.MethodPost || gotPath != "/panel/api/plugins/remote/install" || gotBody != `{"url":"https://github.com/a/b"}` {
		t.Errorf("node saw %s %s %q", gotMethod, gotPath, gotBody)
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("node saw Authorization %q: the master's own credential for that node is what must be used", gotAuth)
	}

	_, _, err = relayTo(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		_, _ = w.Write([]byte(`{"success":true,"msg":"","obj":[]}`))
	}, http.MethodGet, "/panel/api/plugins/logs/hello?lines=50", nil)
	if err != nil || gotPath != "/panel/api/plugins/logs/hello?lines=50" {
		t.Errorf("a query string was lost: %q (%v)", gotPath, err)
	}
}

func TestRelayKeepsTheNodesRefusalsInItsOwnWords(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		wantStatus int
		wantMsg    string
	}{
		{"locked out", http.StatusForbidden, `{"success":false,"msg":"plugin management by the master is disabled on this node","obj":null}`, http.StatusForbidden, "plugin management by the master is disabled on this node"},
		{"not json", http.StatusForbidden, `Forbidden`, http.StatusForbidden, "Forbidden"},
		{"failed command", http.StatusInternalServerError, `{"success":false,"msg":"boom"}`, http.StatusInternalServerError, "boom"},
		{"refusal inside a 200", http.StatusOK, `{"success":false,"msg":"plugin not found","obj":null}`, http.StatusOK, "plugin not found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body, err := relayTo(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}, http.MethodGet, "/panel/api/plugins/list", nil)
			if err != nil {
				t.Fatalf("a refusal is an answer, not a transport error: %v", err)
			}
			if status != tc.wantStatus || !strings.Contains(body, `"success":false`) || !strings.Contains(body, tc.wantMsg) {
				t.Errorf("relay = %d %s, want %d with %q", status, body, tc.wantStatus, tc.wantMsg)
			}
		})
	}
}

func TestRelayReportsAnUnreachableNodeAsAnError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	r := NewRemote(nodeForPlainServer(t, srv, "verify", "tok"), nil)
	srv.Close()
	if status, _, err := RelayPlugin(context.Background(), r, http.MethodGet, "/panel/api/plugins/list", nil); err == nil {
		t.Errorf("relay to a dead node = %d, nil error", status)
	}
}
