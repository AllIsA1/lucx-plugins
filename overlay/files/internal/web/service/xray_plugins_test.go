// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/xray"
)

const baseXrayDoc = `{
  "log": {"loglevel": "warning"},
  "routing": {"rules": [{"type": "field", "inboundTag": ["api"], "outboundTag": "api"}]},
  "inbounds": [
    {"tag": "api", "listen": "127.0.0.1", "port": 62789, "protocol": "tunnel", "settings": {"address": "127.0.0.1"}},
    {"tag": "inbound-443", "listen": "0.0.0.0", "port": 443, "protocol": "vless", "settings": {"clients": []}}
  ],
  "outbounds": [{"tag": "direct", "protocol": "freedom"}, {"tag": "blocked", "protocol": "blackhole"}],
  "policy": {"system": {"statsInboundUplink": true}},
  "api": {"tag": "api", "services": ["HandlerService", "StatsService"]},
  "stats": {},
  "metrics": {"tag": "metrics_out", "listen": "127.0.0.1:11111"}
}`

func edit(t *testing.T, doc string, mutate func(m map[string]any)) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(doc), &m); err != nil {
		t.Fatal(err)
	}
	mutate(m)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestValidateXrayPatch(t *testing.T) {
	cases := []struct {
		name    string
		after   func(m map[string]any)
		wantErr string
	}{
		{"adding a routing rule is fine", func(m map[string]any) {
			r := m["routing"].(map[string]any)
			r["rules"] = append(r["rules"].([]any), map[string]any{"type": "field", "outboundTag": "blocked"})
		}, ""},
		{"adding an outbound is fine", func(m map[string]any) {
			m["outbounds"] = append(m["outbounds"].([]any), map[string]any{"tag": "extra", "protocol": "freedom"})
		}, ""},
		{"adding an inbound is fine", func(m map[string]any) {
			m["inbounds"] = append(m["inbounds"].([]any), map[string]any{"tag": "extra-in", "port": 8443, "protocol": "vless"})
		}, ""},
		{"changing the api section", func(m map[string]any) { m["api"] = map[string]any{"tag": "api", "services": []any{}} }, "api"},
		{"removing the stats section", func(m map[string]any) { delete(m, "stats") }, "stats"},
		{"changing the metrics section", func(m map[string]any) { m["metrics"] = map[string]any{"tag": "x"} }, "metrics"},
		{"removing an existing inbound", func(m map[string]any) { m["inbounds"] = m["inbounds"].([]any)[:1] }, "inbound-443"},
		{"renaming an inbound", func(m map[string]any) { m["inbounds"].([]any)[1].(map[string]any)["tag"] = "renamed" }, "inbound-443"},
		{"touching the api inbound", func(m map[string]any) { m["inbounds"].([]any)[0].(map[string]any)["port"] = 1 }, "api inbound"},
		{"inbounds that are not an array", func(m map[string]any) { m["inbounds"] = "none" }, "inbounds"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			after := edit(t, baseXrayDoc, tc.after)
			err := validateXrayPatch([]byte(baseXrayDoc), []byte(after))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("a harmless patch was refused: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want one mentioning %q", err, tc.wantErr)
			}
		})
	}
	if err := validateXrayPatch([]byte(baseXrayDoc), []byte("{broken")); err == nil {
		t.Error("an unparsable result must be refused")
	}
}

func parseXray(t *testing.T, doc string) *xray.Config {
	t.Helper()
	cfg := &xray.Config{}
	if err := json.Unmarshal([]byte(doc), cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestPatchXrayConfig(t *testing.T) {
	withRule := edit(t, baseXrayDoc, func(m map[string]any) {
		r := m["routing"].(map[string]any)
		r["rules"] = append(r["rules"].([]any), map[string]any{"type": "field", "outboundTag": "blocked"})
	})

	t.Run("a patched document replaces the config", func(t *testing.T) {
		cfg := parseXray(t, baseXrayDoc)
		got := patchXrayConfig(cfg, func(doc []byte, _ func(before, after []byte) error) []byte { return []byte(withRule) })
		if got == cfg || !strings.Contains(string(got.RouterConfig), "blocked") {
			t.Fatalf("routing = %s", got.RouterConfig)
		}
		if len(got.InboundConfigs) != 2 || string(got.API) == "" {
			t.Errorf("unrelated sections must survive the round trip: %d inbounds, api %s", len(got.InboundConfigs), got.API)
		}
	})
	t.Run("an unchanged result returns the very same config", func(t *testing.T) {
		cfg := parseXray(t, baseXrayDoc)
		got := patchXrayConfig(cfg, func(doc []byte, _ func(before, after []byte) error) []byte { return doc })
		if got != cfg {
			t.Error("no plugin changed anything: do not rebuild the config")
		}
	})
	t.Run("garbage from the pipeline is ignored", func(t *testing.T) {
		cfg := parseXray(t, baseXrayDoc)
		for _, out := range []string{"{broken", `{"inbounds":"nope"}`, ""} {
			got := patchXrayConfig(cfg, func([]byte, func(before, after []byte) error) []byte { return []byte(out) })
			if got != cfg {
				t.Errorf("output %q replaced the config", out)
			}
		}
	})
	t.Run("a result that breaks the invariants is refused even if the pipeline let it through", func(t *testing.T) {
		cfg := parseXray(t, baseXrayDoc)
		broken := edit(t, baseXrayDoc, func(m map[string]any) { delete(m, "stats") })
		if got := patchXrayConfig(cfg, func([]byte, func(before, after []byte) error) []byte { return []byte(broken) }); got != cfg {
			t.Error("the panel must re-check the final document itself")
		}
	})
	t.Run("the validator handed to the pipeline is the panel's", func(t *testing.T) {
		cfg := parseXray(t, baseXrayDoc)
		var validate func(before, after []byte) error
		patchXrayConfig(cfg, func(doc []byte, v func(before, after []byte) error) []byte { validate = v; return doc })
		if validate == nil || validate([]byte(baseXrayDoc), []byte(edit(t, baseXrayDoc, func(m map[string]any) { delete(m, "stats") }))) == nil {
			t.Error("the pipeline must be given validateXrayPatch so one bad plugin cannot break the core invariants")
		}
	})
}
