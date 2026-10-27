// Copyright (c) 2025 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package tunnel

import (
	"strings"
	"testing"
)

func TestDefaultOlcrtcConfig(t *testing.T) {
	cfg := DefaultOlcrtcConfig()
	if cfg.Provider != "jitsi" || cfg.Transport != "datachannel" {
		t.Fatalf("defaults = %+v", cfg)
	}
	if cfg.DNS != "8.8.8.8:53" || cfg.VP8Fps != 60 || cfg.VP8Batch != 64 {
		t.Fatalf("defaults = %+v", cfg)
	}
}

func TestOlcrtcValidate(t *testing.T) {
	base := func() OlcrtcConfig {
		c := DefaultOlcrtcConfig()
		c.RoomID = "https://meet.example.org/room"
		c.CryptoKey = strings.Repeat("ab", 32)
		return c
	}
	if err := base().Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*OlcrtcConfig)
	}{
		{"no room", func(c *OlcrtcConfig) { c.RoomID = " " }},
		{"bad provider", func(c *OlcrtcConfig) { c.Provider = "zoom" }},
		{"bad transport", func(c *OlcrtcConfig) { c.Transport = "udp" }},
		{"telemost+datachannel", func(c *OlcrtcConfig) { c.Provider = "telemost"; c.Transport = "datachannel" }},
		{"telemost+seichannel", func(c *OlcrtcConfig) { c.Provider = "telemost"; c.Transport = "seichannel" }},
		{"wbstream+datachannel", func(c *OlcrtcConfig) { c.Provider = "wbstream"; c.Transport = "datachannel" }},
		{"dns bare ip", func(c *OlcrtcConfig) { c.DNS = "8.8.8.8" }},
		{"bad key", func(c *OlcrtcConfig) { c.CryptoKey = "short" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base()
			tc.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	// Empty key is OK at Validate — EnsureCryptoKey fills it on save.
	emptyKey := base()
	emptyKey.CryptoKey = ""
	if err := emptyKey.Validate(); err != nil {
		t.Fatalf("empty key must pass Validate: %v", err)
	}
}

func TestOlcrtcRenderYAML(t *testing.T) {
	cfg := DefaultOlcrtcConfig()
	cfg.RoomID = "https://meet.example.org/r"
	cfg.CryptoKey = strings.Repeat("cd", 32)
	cfg.Debug = true
	got := cfg.RenderYAML("/tmp/olcrtc-data")
	for _, want := range []string{
		"mode: srv",
		`provider: "jitsi"`,
		`id: "https://meet.example.org/r"`,
		`key: "` + cfg.CryptoKey + `"`,
		`transport: "datachannel"`,
		`dns: "8.8.8.8:53"`,
		"debug: true",
		"interval: 10s",
		"timeout: 15s",
		"failures: 4",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("YAML missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "data:") {
		t.Errorf("OLC2 treats data: as a names-file override; omit it:\n%s", got)
	}
	if strings.Contains(got, "vp8:") {
		t.Errorf("datachannel must not render vp8 block:\n%s", got)
	}

	cfg.Transport = "vp8channel"
	cfg.VP8Fps = 30
	cfg.VP8Batch = 16
	got = cfg.RenderYAML("")
	for _, want := range []string{
		`transport: "vp8channel"`,
		"vp8:",
		"fps: 30",
		"batch_size: 16",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("vp8 YAML missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "data:") {
		t.Errorf("empty dataDir must omit data key:\n%s", got)
	}

	cfg.Transport = "seichannel"
	cfg.SeiFps, cfg.SeiBatch, cfg.SeiFrag, cfg.SeiAck = 30, 64, 900, 2000
	got = cfg.RenderYAML("")
	for _, want := range []string{
		`transport: "seichannel"`,
		"sei:",
		"fragment_size: 900",
		"ack_timeout_ms: 2000",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("sei YAML missing %q:\n%s", want, got)
		}
	}

	cfg.Transport = "videochannel"
	cfg.VideoW, cfg.VideoH, cfg.VideoFps, cfg.VideoCodec = 1080, 1080, 30, "qrcode"
	got = cfg.RenderYAML("")
	for _, want := range []string{
		`transport: "videochannel"`,
		"video:",
		"width: 1080",
		`codec: "qrcode"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("video YAML missing %q:\n%s", want, got)
		}
	}
}

func TestOlcrtcTransportMatrix(t *testing.T) {
	ok := func(provider, transport string) {
		t.Helper()
		c := DefaultOlcrtcConfig()
		c.RoomID = "r"
		c.Provider, c.Transport = provider, transport
		if err := c.Validate(); err != nil {
			t.Fatalf("%s+%s: %v", provider, transport, err)
		}
	}
	ok("jitsi", "seichannel")
	ok("jitsi", "videochannel")
	ok("telemost", "videochannel")
	ok("wbstream", "seichannel")
	ok("wbstream", "videochannel")
	ok("wbstream", "vp8channel")
}

func TestOlcrtcClientURI(t *testing.T) {
	cfg := DefaultOlcrtcConfig()
	cfg.RoomID = "https://meet.example.org/r"
	cfg.CryptoKey = strings.Repeat("ef", 32)
	got := cfg.ClientURI()
	want := "olcrtc://jitsi?datachannel@https://meet.example.org/r#" + cfg.CryptoKey
	if got != want {
		t.Fatalf("URI = %q, want %q", got, want)
	}

	cfg.Transport = "vp8channel"
	cfg.VP8Fps = 60
	cfg.VP8Batch = 64
	got = cfg.ClientURI()
	if !strings.Contains(got, "vp8channel<vp8-fps=60&vp8-batch=64>") {
		t.Fatalf("vp8 URI = %q", got)
	}

	cfg.Transport = "seichannel"
	cfg.SeiFps, cfg.SeiBatch, cfg.SeiFrag, cfg.SeiAck = 30, 64, 900, 2000
	got = cfg.ClientURI()
	if !strings.Contains(got, "seichannel<fps=30&batch=64&frag=900&ack-ms=2000>") {
		t.Fatalf("sei URI = %q", got)
	}

	cfg.Transport = "videochannel"
	cfg.VideoW, cfg.VideoH, cfg.VideoFps, cfg.VideoCodec = 1080, 1080, 30, "qrcode"
	got = cfg.ClientURI()
	if !strings.Contains(got, "videochannel<video-w=1080&video-h=1080&video-fps=30&video-codec=qrcode>") {
		t.Fatalf("video URI = %q", got)
	}

	cfg.RoomID = ""
	if cfg.ClientURI() != "" {
		t.Fatal("empty room must yield empty URI")
	}
}

func TestOlcrtcEnsureCryptoKey(t *testing.T) {
	cfg := DefaultOlcrtcConfig()
	cfg.RoomID = "room"
	got, err := cfg.EnsureCryptoKey()
	if err != nil {
		t.Fatal(err)
	}
	if !validCryptoKey(got.CryptoKey) {
		t.Fatalf("generated key invalid: %q", got.CryptoKey)
	}
	// Second call keeps the existing key.
	keep := got.CryptoKey
	got2, err := got.EnsureCryptoKey()
	if err != nil || got2.CryptoKey != keep {
		t.Fatalf("EnsureCryptoKey must keep existing key, got %q", got2.CryptoKey)
	}
}

func TestOlcrtcYAMLEscapes(t *testing.T) {
	cfg := DefaultOlcrtcConfig()
	cfg.RoomID = `room"quote`
	cfg.CryptoKey = strings.Repeat("11", 32)
	got := cfg.RenderYAML("")
	if !strings.Contains(got, `id: "room\"quote"`) {
		t.Fatalf("room quote not escaped:\n%s", got)
	}
}

func forkBase() OlcrtcConfig {
	c := DefaultOlcrtcConfig()
	c.RoomID = "room"
	c.CryptoKey = strings.Repeat("ab", 32)
	return c
}

func TestOlcrtcForkProviderMatrix(t *testing.T) {
	cases := []struct {
		provider, transport string
		token               bool
		ok                  bool
	}{
		{"salutejazz", "datachannel", false, true},
		{"salutejazz", "vp8channel", false, false},
		{"salutejazz", "videochannel", false, false},
		{"vkcalls", "vp8channel", false, true},
		{"vkcalls", "datachannel", false, false},
		{"vkcalls", "seichannel", false, false},
		{"mtslink", "vp8channel", false, true},
		{"mtslink", "videochannel", false, false},
		{"wbstream", "datachannel", false, false},
		{"wbstream", "datachannel", true, true},
		{"jitsi", "datachannel", false, true},
		{"telemost", "datachannel", true, false},
		{"telemost", "seichannel", false, false},
	}
	for _, tc := range cases {
		c := forkBase()
		c.Provider, c.Transport = tc.provider, tc.transport
		if tc.token {
			c.AuthToken = "token"
		}
		if err := c.Validate(); (err == nil) != tc.ok {
			t.Errorf("%s+%s token=%v: err = %v, want ok=%v", tc.provider, tc.transport, tc.token, err, tc.ok)
		}
	}
}

func TestCoerceOlcrtcTransportKeepsToTheProviderMatrix(t *testing.T) {
	for _, tc := range []struct{ provider, transport, want string }{
		{"salutejazz", "videochannel", "datachannel"},
		{"vkcalls", "datachannel", "vp8channel"},
		{"mtslink", "seichannel", "vp8channel"},
		{"telemost", "datachannel", "vp8channel"},
		{"jitsi", "seichannel", "seichannel"},
	} {
		c := forkBase()
		c.Provider, c.Transport = tc.provider, tc.transport
		if got := CoerceOlcrtcTransport(c).Transport; got != tc.want {
			t.Errorf("%s+%s coerced to %s, want %s", tc.provider, tc.transport, got, tc.want)
		}
	}
}

// The YAML of a config that sets none of the fork's settings is what the panel always wrote, so the original core
// (which refuses keys it does not know) keeps starting.
func TestOlcrtcYAMLWithoutForkSettingsIsUnchanged(t *testing.T) {
	c := forkBase()
	c.Provider, c.Transport = "jitsi", "datachannel"
	c.RoomID = "https://meet.example.org/r"
	want := "mode: srv\nauth:\n  provider: \"jitsi\"\nroom:\n  id: \"https://meet.example.org/r\"\n" +
		"crypto:\n  key: \"" + c.CryptoKey + "\"\nnet:\n  transport: \"datachannel\"\n  dns: \"8.8.8.8:53\"\n" +
		"liveness:\n  interval: 10s\n  timeout: 15s\n  failures: 4\ndebug: false\n"
	if got := c.RenderYAML(""); got != want {
		t.Fatalf("YAML changed:\n%s\nwant:\n%s", got, want)
	}
}

func TestOlcrtcYAMLForkSettings(t *testing.T) {
	c := forkBase()
	c.Provider, c.Transport = "wbstream", "videochannel"
	c.VideoW, c.VideoH, c.VideoFps, c.VideoCodec = 1080, 1080, 30, "qrcode"
	c.VideoQRSize, c.VideoQRRecovery = 200, "medium"
	c.AuthToken = "tok"
	c.ExtraKeys = []string{strings.Repeat("cd", 32), strings.Repeat("CD", 32), c.CryptoKey, " "}
	c.DTLSProfile = olcrtcDTLSChrome
	c.UDPEnabled, c.UDPMaxFlows = true, 64
	c.LivenessInterval, c.LivenessTimeout, c.LivenessFailures = "5s", "20s", 6
	c.MaxSessionDuration = "6h"
	c.TrafficMaxPayload, c.TrafficMinDelay, c.TrafficMaxDelay = 4096, "5ms", "30ms"
	c = c.ClampVP8()
	got := c.RenderYAML("")
	for _, want := range []string{
		"  provider: \"wbstream\"\n  token: \"tok\"\n",
		"crypto:\n  keys:\n    - \"" + c.CryptoKey + "\"\n    - \"" + strings.Repeat("cd", 32) + "\"\n",
		"dtls:\n  profile: \"chrome-linux-138-compat-v1\"\n",
		"  qr_size: 200\n", "  qr_recovery: \"medium\"\n",
		"udp:\n  enabled: true\n  max_flows: 64\n",
		"liveness:\n  interval: 5s\n  timeout: 20s\n  failures: 6\n",
		"lifecycle:\n  max_session_duration: \"6h\"\n",
		"traffic:\n  max_payload_size: 4096\n  min_delay: \"5ms\"\n  max_delay: \"30ms\"\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("YAML missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "  key: ") {
		t.Errorf("crypto.key and crypto.keys must not both be written:\n%s", got)
	}
	if strings.Count(got, strings.Repeat("cd", 32)) != 1 {
		t.Errorf("a repeated extra key was written twice:\n%s", got)
	}

	c.VideoCodec = "tile"
	c.VideoTileModule, c.VideoTileRS = 8, 20
	got = c.ClampVP8().RenderYAML("")
	if !strings.Contains(got, "  tile_module: 8\n") || !strings.Contains(got, "  tile_rs: 20\n") || strings.Contains(got, "qr_size") {
		t.Errorf("tile YAML:\n%s", got)
	}
}

func TestOlcrtcForkSettingsAreChecked(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*OlcrtcConfig)
	}{
		{"short extra key", func(c *OlcrtcConfig) { c.ExtraKeys = []string{"abcd"} }},
		{"extra keys without a main key", func(c *OlcrtcConfig) { c.CryptoKey = ""; c.ExtraKeys = []string{strings.Repeat("cd", 32)} }},
		{"unknown dtls profile", func(c *OlcrtcConfig) { c.DTLSProfile = "firefox" }},
		{"negative udp flows", func(c *OlcrtcConfig) { c.UDPMaxFlows = -1 }},
		{"liveness interval not a duration", func(c *OlcrtcConfig) { c.LivenessInterval = "often" }},
		{"liveness interval zero", func(c *OlcrtcConfig) { c.LivenessInterval = "0s" }},
		{"liveness timeout shorter than interval", func(c *OlcrtcConfig) { c.LivenessInterval = "20s"; c.LivenessTimeout = "5s" }},
		{"negative failures", func(c *OlcrtcConfig) { c.LivenessFailures = -2 }},
		{"session duration negative", func(c *OlcrtcConfig) { c.MaxSessionDuration = "-1h" }},
		{"negative payload", func(c *OlcrtcConfig) { c.TrafficMaxPayload = -1 }},
		{"max delay below min", func(c *OlcrtcConfig) { c.TrafficMinDelay = "30ms"; c.TrafficMaxDelay = "5ms" }},
		{"unknown qr recovery", func(c *OlcrtcConfig) { c.VideoQRRecovery = "max" }},
		{"tile module too big", func(c *OlcrtcConfig) { c.VideoTileModule = 300 }},
		{"tile rs too big", func(c *OlcrtcConfig) { c.VideoTileRS = 201 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := forkBase()
			tc.mutate(&c)
			if err := c.ValidateExtras(); err == nil {
				t.Fatal("ValidateExtras accepted it")
			}
			if err := c.Validate(); err == nil {
				t.Fatal("Validate accepted it")
			}
		})
	}
	ok := forkBase()
	ok.ExtraKeys = []string{strings.Repeat("cd", 32)}
	ok.DTLSProfile, ok.UDPEnabled = "off", true
	ok.LivenessInterval, ok.LivenessTimeout = "10s", "15s"
	ok.MaxSessionDuration, ok.TrafficMinDelay, ok.TrafficMaxDelay = "30m", "0s", "30ms"
	if err := ok.Validate(); err != nil {
		t.Fatalf("a valid set of settings was refused: %v", err)
	}
}

func TestOlcrtcClientURIName(t *testing.T) {
	c := forkBase()
	c.Provider, c.RoomID = "vkcalls", "https://vk.ru/call/join/abc"
	c.Transport, c.VP8Fps, c.VP8Batch = "vp8channel", 30, 64
	plain := "olcrtc://vkcalls?vp8channel<vp8-fps=30&vp8-batch=64>@https://vk.ru/call/join/abc#" + c.CryptoKey
	if got := c.ClientURI(); got != plain {
		t.Fatalf("no remark: %q, want %q", got, plain)
	}
	c.Remark = "  Poland $1\n\tfast  "
	if got := c.ClientURI(); got != plain+"$Poland -1 fast" {
		t.Errorf("remark: %q", got)
	}
	c.NoLinkName = true
	if got := c.ClientURI(); got != plain {
		t.Errorf("NoLinkName: %q", got)
	}
	c.NoLinkName = false
	c.Remark = strings.Repeat("я", 150)
	if got := c.ClientURI(); !strings.HasSuffix(got, "$"+strings.Repeat("я", 100)) {
		t.Errorf("a long remark was not cut to 100 characters: %q", got)
	}
}

func TestOlcrtcClientURIVideoParams(t *testing.T) {
	c := forkBase()
	c.Transport, c.VideoW, c.VideoH, c.VideoFps, c.VideoCodec = "videochannel", 1080, 1080, 30, "qrcode"
	c.VideoQRSize, c.VideoQRRecovery = 300, "high"
	if got := c.ClientURI(); !strings.Contains(got, "videochannel<video-w=1080&video-h=1080&video-fps=30&video-codec=qrcode&video-qr-size=300&video-qr-recovery=high>@") {
		t.Errorf("qrcode URI = %q", got)
	}
	c.VideoCodec, c.VideoTileModule, c.VideoTileRS = "tile", 6, 25
	if got := c.ClientURI(); !strings.Contains(got, "video-codec=tile&video-tile-module=6&video-tile-rs=25>@") || strings.Contains(got, "qr-") {
		t.Errorf("tile URI = %q", got)
	}
}
