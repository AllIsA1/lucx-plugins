// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/runtime"
)

func warpInbound(settings string) *model.Inbound {
	return &model.Inbound{Protocol: model.Warp, Port: 4242, Settings: settings}
}

func TestWarpSettingsGetDefaultsAndAPortlessRow(t *testing.T) {
	ib := warpInbound(`{"clients":[{"email":"a@x"}]}`)
	if err := (&InboundService{}).normalizeWarpSettings(ib); err != nil {
		t.Fatal(err)
	}
	if ib.Port != 0 {
		t.Errorf("a WARP row binds nothing: port %d", ib.Port)
	}
	var s map[string]any
	if err := json.Unmarshal([]byte(ib.Settings), &s); err != nil {
		t.Fatal(err)
	}
	awg := s["awg"].(map[string]any)
	if s["mtu"] != float64(1280) || s["keepalive"] != float64(25) || s["ipv6"] != true || s["preset"] != "custom" || awg["jc"] != float64(4) || awg["jmax"] != float64(70) {
		t.Errorf("defaults = %v", s)
	}
	if len(s["endpoints"].([]any)) != 0 {
		t.Error("no endpoints means the plugin's own")
	}
	if cs, _ := s["clients"].([]any); len(cs) != 1 {
		t.Error("the clients must survive")
	}
	if !strings.Contains(s["dns"].(string), "1.1.1.1") {
		t.Errorf("dns = %v", s["dns"])
	}
}

func TestWarpPresetsSetTheJunk(t *testing.T) {
	ib := warpInbound(`{"preset":"strong","awg":{"jc":1,"jmin":1,"jmax":1,"s1":5}}`)
	if err := (&InboundService{}).normalizeWarpSettings(ib); err != nil {
		t.Fatal(err)
	}
	var s struct {
		AWG map[string]float64 `json:"awg"`
	}
	_ = json.Unmarshal([]byte(ib.Settings), &s)
	if s.AWG["jc"] != 8 || s.AWG["jmin"] != 50 || s.AWG["jmax"] != 150 || s.AWG["s1"] != 0 {
		t.Errorf("a preset wins over the fields: %v", s.AWG)
	}
}

func TestWarpSettingsRefuseWhatWouldBreakALine(t *testing.T) {
	for name, settings := range map[string]string{
		"not json":          `{`,
		"endpoint no port":  `{"endpoints":["162.159.192.1"]}`,
		"endpoint a space":  `{"endpoints":["a b:2408"]}`,
		"four endpoints":    `{"endpoints":["a.b:1","a.b:2","a.b:3","a.b:4"]}`,
		"mtu small":         `{"mtu":100}`,
		"mtu large":         `{"mtu":9000}`,
		"dns a name":        `{"dns":"one.one.one.one"}`,
		"dns a line break":  `{"dns":"1.1.1.1\n[Peer]"}`,
		"keepalive":         `{"keepalive":-1}`,
		"bad preset":        `{"preset":"nope"}`,
		"too much junk":     `{"preset":"custom","awg":{"jc":99,"jmin":1,"jmax":2}}`,
		"jmax under jmin":   `{"preset":"custom","awg":{"jc":4,"jmin":50,"jmax":10}}`,
		"i1 a line break":   `{"preset":"custom","awg":{"jc":4,"jmin":1,"jmax":2,"i1":"<b 0x00>\n[Peer]"}}`,
		"name a line break": `{"name":"a\nb"}`,
		"padding too big":   `{"preset":"custom","awg":{"jc":4,"jmin":1,"jmax":2,"s1":5000}}`,
	} {
		if err := (&InboundService{}).normalizeWarpSettings(warpInbound(settings)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestWarpSettingsOfOtherProtocolsAreLeftAlone(t *testing.T) {
	ib := &model.Inbound{Protocol: model.VLESS, Port: 443, Settings: `{"mtu":5}`}
	if err := (&InboundService{}).normalizeWarpSettings(ib); err != nil || ib.Port != 443 || ib.Settings != `{"mtu":5}` {
		t.Errorf("%v %+v", err, ib)
	}
}

func TestWarpIsServedOutsideXrayAndNeverConflictsOnAPort(t *testing.T) {
	if !inboundHasSidecar(model.Warp) || !lucxRuntimeSidecar(model.Warp) {
		t.Error("a WARP row must stay out of the generated Xray config")
	}
	if nodeEligibleProtocols[model.Warp] {
		t.Error("WARP rows are not pushed to nodes")
	}
}

// A WARP row is created without a port, runs nothing and never asks the runtime to bind anything.
func TestAddingAndEditingAWarpInboundTouchesNoListener(t *testing.T) {
	setupConflictDB(t)
	mgr := runtime.NewManager(runtime.LocalDeps{APIPort: func() int { return 0 }})
	rec := &recordingRuntime{}
	mgr.SetLocalRuntimeOverride(rec)
	runtime.SetManager(mgr)
	t.Cleanup(func() { runtime.SetManager(nil) })

	svc := &InboundService{}
	created, _, err := svc.AddInbound(&model.Inbound{
		Tag: "warp-a", Remark: "WARP", Enable: true, Protocol: model.Warp, Port: 8443,
		Settings: `{"endpoints":["162.159.192.1:2408"],"preset":"light","clients":[]}`,
	})
	if err != nil {
		t.Fatalf("AddInbound: %v", err)
	}
	row := loadInboundByTag(t, "warp-a")
	if row.Port != 0 || created.Port != 0 {
		t.Errorf("port = %d, want 0: a WARP row binds nothing", row.Port)
	}
	if awg, _ := warpSettingsOf(t, row)["awg"].(map[string]any); awg["jc"] != float64(2) {
		t.Errorf("the preset was not applied: %s", row.Settings)
	}

	// A second WARP row does not collide with the first on port 0, nor with a real listener on the old port.
	if _, _, err := svc.AddInbound(&model.Inbound{Tag: "warp-b", Enable: true, Protocol: model.Warp, Port: 8443, Settings: `{}`}); err != nil {
		t.Fatalf("a second WARP inbound: %v", err)
	}

	update := *loadInboundByTag(t, "warp-a")
	update.Remark = "renamed"
	update.Settings = `{"mtu":1380}`
	if _, _, err := svc.UpdateInbound(&update); err != nil {
		t.Fatalf("UpdateInbound: %v", err)
	}
	if got := loadInboundByTag(t, "warp-a"); warpSettingsOf(t, got)["mtu"] != float64(1380) || got.Port != 0 {
		t.Errorf("update = %+v", got)
	}

	bad := *loadInboundByTag(t, "warp-a")
	bad.Settings = `{"endpoints":["not an endpoint"]}`
	if _, _, err := svc.UpdateInbound(&bad); err == nil {
		t.Error("settings the plugin could not use were saved")
	}
}

func warpSettingsOf(t *testing.T, ib *model.Inbound) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(ib.Settings), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestWarpPassesTheInboundBindingValidation(t *testing.T) {
	err := validator.New().Struct(&model.Inbound{Protocol: model.Warp, Port: 0, ShareAddrStrategy: "node"})
	if err != nil {
		t.Errorf("the controller would refuse a WARP inbound: %v", err)
	}
}

// Portless rows get an automatic tag; two of them must not collide on it.
func TestTwoWarpInboundsGetDistinctAutomaticTags(t *testing.T) {
	setupConflictDB(t)
	mgr := runtime.NewManager(runtime.LocalDeps{APIPort: func() int { return 0 }})
	mgr.SetLocalRuntimeOverride(&recordingRuntime{})
	runtime.SetManager(mgr)
	t.Cleanup(func() { runtime.SetManager(nil) })
	svc := &InboundService{}
	a, _, err := svc.AddInbound(&model.Inbound{Enable: true, Protocol: model.Warp, Settings: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := svc.AddInbound(&model.Inbound{Enable: true, Protocol: model.Warp, Settings: `{}`})
	if err != nil {
		t.Fatalf("the second WARP inbound: %v", err)
	}
	if a.Tag == "" || a.Tag == b.Tag {
		t.Errorf("tags %q and %q", a.Tag, b.Tag)
	}
}
