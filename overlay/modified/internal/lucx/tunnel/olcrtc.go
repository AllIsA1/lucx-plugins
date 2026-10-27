// Copyright (c) 2025 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package tunnel

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"
)

// OlcrtcConfig is the operator-facing configuration of the olcRTC core
// (openlibrecommunity/olcrtc). The panel renders it into a server YAML that
// the binary takes as its sole CLI argument. Field names and validation
// mirror Bebrik2283555/Ex3-ui's extras integration and the upstream
// docs/examples/server/*.yaml schema.
type OlcrtcConfig struct {
	Remark  string `json:"remark"`
	Enabled bool   `json:"enabled"`

	// Provider is the meet auth backend: jitsi | telemost | wbstream.
	Provider string `json:"provider"`
	// RoomID is the full room URL (jitsi) or room identifier (telemost/wb).
	RoomID string `json:"roomId"`
	// CryptoKey is 64 hex chars (32 bytes), shared with the client. Empty
	// on save is auto-generated.
	CryptoKey string `json:"cryptoKey"`
	// Transport: datachannel | vp8channel | seichannel | videochannel.
	Transport string `json:"transport"`
	DNS       string `json:"dns"`

	VP8Fps   int `json:"vp8Fps"`
	VP8Batch int `json:"vp8Batch"`

	SeiFps   int `json:"seiFps"`
	SeiBatch int `json:"seiBatch"`
	SeiFrag  int `json:"seiFrag"`
	SeiAck   int `json:"seiAck"`

	VideoW     int    `json:"videoW"`
	VideoH     int    `json:"videoH"`
	VideoFps   int    `json:"videoFps"`
	VideoCodec string `json:"videoCodec"`
	// Video settings of the ghostlane-project fork; zero means the core's own default and is not written.
	VideoQRSize     int    `json:"videoQrSize,omitempty"`     // video.qr_size, bytes per QR fragment (qrcode)
	VideoQRRecovery string `json:"videoQrRecovery,omitempty"` // video.qr_recovery: low | medium | high | highest (qrcode)
	VideoTileModule int    `json:"videoTileModule,omitempty"` // video.tile_module, 1..270 (tile)
	VideoTileRS     int    `json:"videoTileRs,omitempty"`     // video.tile_rs, Reed-Solomon parity %, 0..200 (tile)

	Debug bool `json:"debug"`

	// Settings of the ghostlane-project fork (the core the Better olcRTC plugin installs). Every one is optional and
	// is written into the server YAML only when set, so a panel on the original core keeps producing the YAML it did.
	AuthToken   string   `json:"authToken,omitempty"`   // auth.token: a pre-issued provider account token (WB Stream moderator, ...)
	ExtraKeys   []string `json:"extraKeys,omitempty"`   // crypto.keys: more keys the server accepts next to CryptoKey
	DTLSProfile string   `json:"dtlsProfile,omitempty"` // dtls.profile: "" or "off" keeps the stock handshake
	UDPEnabled  bool     `json:"udpEnabled,omitempty"`  // udp.enabled: the lossy SOCKS5 UDP relay (calls, games)
	UDPMaxFlows int      `json:"udpMaxFlows,omitempty"` // udp.max_flows, 0 = the core's default (1024)

	LivenessInterval   string `json:"livenessInterval,omitempty"`   // liveness.interval, Go duration (default 10s)
	LivenessTimeout    string `json:"livenessTimeout,omitempty"`    // liveness.timeout (default 15s)
	LivenessFailures   int    `json:"livenessFailures,omitempty"`   // liveness.failures (default 4)
	MaxSessionDuration string `json:"maxSessionDuration,omitempty"` // lifecycle.max_session_duration, e.g. 6h; empty = never rebuilt
	TrafficMaxPayload  int    `json:"trafficMaxPayload,omitempty"`  // traffic.max_payload_size, 0 = the transport's limit
	TrafficMinDelay    string `json:"trafficMinDelay,omitempty"`    // traffic.min_delay, e.g. 5ms
	TrafficMaxDelay    string `json:"trafficMaxDelay,omitempty"`    // traffic.max_delay, e.g. 30ms

	// NoLinkName leaves the inbound's name out of the olcrtc:// link (the "$name" tail of the URI convention), for a
	// client that does not know it.
	NoLinkName bool `json:"noLinkName,omitempty"`

	// RouteThroughXray: dial egress (and transport HTTP) via SOCKS bridge in
	// Xray (native socks: YAML). Default false — Telemost/Jitsi need direct
	// path; SOCKS also cannot carry ICMP so client "ping" always fails when on.
	RouteThroughXray bool   `json:"routeThroughXray"`
	OutboundTag      string `json:"outboundTag"`
	// RouteXrayPort is backend-owned loopback SOCKS port (0 when not routed).
	RouteXrayPort int `json:"routeXrayPort"`

	MigratedToInbound bool `json:"migratedToInbound,omitempty"`
	MigratedInboundId int  `json:"migratedInboundId,omitempty"`
}

// DefaultOlcrtcConfig returns sensible defaults for a fresh olcRTC core.
func DefaultOlcrtcConfig() OlcrtcConfig {
	return OlcrtcConfig{
		Provider:   "jitsi",
		Transport:  "datachannel",
		DNS:        "8.8.8.8:53",
		VP8Fps:     60,
		VP8Batch:   64,
		SeiFps:     30,
		SeiBatch:   64,
		SeiFrag:    900,
		SeiAck:     2000,
		VideoW:     1080,
		VideoH:     1080,
		VideoFps:   30,
		VideoCodec: "qrcode",
	}
}

// Merge fills zero fields of c from the defaults.
func (c OlcrtcConfig) Merge() OlcrtcConfig {
	def := DefaultOlcrtcConfig()
	if c.Provider == "" {
		c.Provider = def.Provider
	}
	if c.Transport == "" {
		c.Transport = def.Transport
	}
	if c.DNS == "" {
		c.DNS = def.DNS
	}
	if c.VP8Fps == 0 {
		c.VP8Fps = def.VP8Fps
	}
	if c.VP8Batch == 0 {
		c.VP8Batch = def.VP8Batch
	}
	if c.SeiFps == 0 {
		c.SeiFps = def.SeiFps
	}
	if c.SeiBatch == 0 {
		c.SeiBatch = def.SeiBatch
	}
	if c.SeiFrag == 0 {
		c.SeiFrag = def.SeiFrag
	}
	if c.SeiAck == 0 {
		c.SeiAck = def.SeiAck
	}
	if c.VideoW == 0 {
		c.VideoW = def.VideoW
	}
	if c.VideoH == 0 {
		c.VideoH = def.VideoH
	}
	if c.VideoFps == 0 {
		c.VideoFps = def.VideoFps
	}
	if c.VideoCodec == "" {
		c.VideoCodec = def.VideoCodec
	}
	return c
}

// olcrtcProviders are the meet backends the core takes: jitsi, telemost and wbstream of the original, and the three the
// ghostlane-project fork adds (docs/settings.md of the fork).
var olcrtcProviders = []string{"jitsi", "telemost", "wbstream", "salutejazz", "vkcalls", "mtslink"}

const (
	olcrtcDTLSOff    = "off"
	olcrtcDTLSChrome = "chrome-linux-138-compat-v1"
)

// olcrtcTransportOK is the fork's compatibility matrix of provider and transport. A WB Stream guest gets a token that
// cannot publish data, so datachannel there works only with an account token (auth.token).
func olcrtcTransportOK(provider, transport string, hasToken bool) bool {
	switch transport {
	case "datachannel":
		return provider == "jitsi" || provider == "salutejazz" || (provider == "wbstream" && hasToken)
	case "vp8channel":
		return provider == "jitsi" || provider == "telemost" || provider == "wbstream" || provider == "vkcalls" || provider == "mtslink"
	case "videochannel":
		return provider == "jitsi" || provider == "telemost" || provider == "wbstream"
	case "seichannel":
		return provider == "jitsi" || provider == "wbstream"
	default:
		return false
	}
}

// CoerceOlcrtcTransport replaces a transport the provider cannot carry by one it can.
func CoerceOlcrtcTransport(c OlcrtcConfig) OlcrtcConfig {
	hasToken := strings.TrimSpace(c.AuthToken) != ""
	if olcrtcTransportOK(c.Provider, c.Transport, hasToken) {
		return c
	}
	c.Transport = "vp8channel"
	if !olcrtcTransportOK(c.Provider, c.Transport, hasToken) {
		c.Transport = "datachannel" // SaluteJazz carries data channels only
	}
	return c
}

// Validate checks the config for internal consistency against the upstream
// olcrtc binary and the olcbox Android client capabilities.
func (c OlcrtcConfig) Validate() error {
	if strings.TrimSpace(c.RoomID) == "" {
		return errors.New("olcrtc: room id is required")
	}
	if !slices.Contains(olcrtcProviders, c.Provider) {
		return errors.New("olcrtc: provider must be " + strings.Join(olcrtcProviders, " | "))
	}
	switch c.Transport {
	case "datachannel", "vp8channel", "seichannel", "videochannel":
	default:
		return errors.New("olcrtc: transport must be datachannel | vp8channel | seichannel | videochannel")
	}
	if !olcrtcTransportOK(c.Provider, c.Transport, strings.TrimSpace(c.AuthToken) != "") {
		if c.Provider == "wbstream" && c.Transport == "datachannel" {
			return errors.New("olcrtc: wbstream carries datachannel only with an account token (auth token)")
		}
		return errors.New("olcrtc: " + c.Provider + " does not support " + c.Transport)
	}
	if c.Transport == "videochannel" {
		switch c.VideoCodec {
		case "", "qrcode", "tile":
		default:
			return errors.New("olcrtc: video codec must be qrcode | tile")
		}
	}
	if strings.TrimSpace(c.DNS) == "" {
		return errors.New("olcrtc: dns must be set (host:port)")
	}
	if _, _, err := net.SplitHostPort(c.DNS); err != nil {
		return errors.New("olcrtc: dns must be in host:port form (e.g. 8.8.8.8:53)")
	}
	if key := strings.TrimSpace(c.CryptoKey); key != "" && !validCryptoKey(key) {
		return errors.New("olcrtc: crypto key must be 64 hex chars (openssl rand -hex 32)")
	}
	return c.validateFork()
}

// ValidateExtras checks only the optional settings of the fork (the ones a save must refuse when they are wrong,
// while a missing room or key is still left to the instance, which then stays off).
func (c OlcrtcConfig) ValidateExtras() error { return c.validateFork() }

// validateFork checks the settings of the fork; each is optional.
func (c OlcrtcConfig) validateFork() error {
	for _, k := range c.ExtraKeys {
		if !validCryptoKey(strings.TrimSpace(k)) {
			return errors.New("olcrtc: every extra key must be 64 hex chars (openssl rand -hex 32)")
		}
	}
	if len(c.ExtraKeys) > 0 && strings.TrimSpace(c.CryptoKey) == "" {
		return errors.New("olcrtc: extra keys need the main key")
	}
	switch c.DTLSProfile {
	case "", olcrtcDTLSOff, olcrtcDTLSChrome:
	default:
		return errors.New("olcrtc: dtls profile must be empty, off or " + olcrtcDTLSChrome)
	}
	if c.UDPMaxFlows < 0 {
		return errors.New("olcrtc: udp max flows must be 0 or a positive number")
	}
	interval, err := positiveDuration(c.LivenessInterval, "liveness interval")
	if err != nil {
		return err
	}
	timeout, err := positiveDuration(c.LivenessTimeout, "liveness timeout")
	if err != nil {
		return err
	}
	if interval > 0 && timeout > 0 && timeout < interval {
		return errors.New("olcrtc: liveness timeout must not be shorter than the interval")
	}
	if c.LivenessFailures < 0 {
		return errors.New("olcrtc: liveness failures must be 0 or a positive number")
	}
	if _, err := positiveDuration(c.MaxSessionDuration, "max session duration"); err != nil {
		return err
	}
	if c.TrafficMaxPayload < 0 {
		return errors.New("olcrtc: traffic max payload must be 0 or a positive number")
	}
	minDelay, err := nonNegativeDuration(c.TrafficMinDelay, "traffic min delay")
	if err != nil {
		return err
	}
	maxDelay, err := nonNegativeDuration(c.TrafficMaxDelay, "traffic max delay")
	if err != nil {
		return err
	}
	if maxDelay < minDelay {
		return errors.New("olcrtc: traffic max delay must not be shorter than the min delay")
	}
	switch c.VideoQRRecovery {
	case "", "low", "medium", "high", "highest":
	default:
		return errors.New("olcrtc: video qr recovery must be low | medium | high | highest")
	}
	if c.VideoQRSize < 0 {
		return errors.New("olcrtc: video qr size must be 0 (auto) or a positive number")
	}
	if c.VideoTileModule < 0 || c.VideoTileModule > 270 {
		return errors.New("olcrtc: video tile module must be within 1..270 (0 = default)")
	}
	if c.VideoTileRS < 0 || c.VideoTileRS > 200 {
		return errors.New("olcrtc: video tile rs must be within 0..200")
	}
	return nil
}

// positiveDuration parses an optional Go duration that must be above zero when given.
func positiveDuration(v, name string) (time.Duration, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, errors.New("olcrtc: " + name + " must be a duration above zero, like 10s")
	}
	return d, nil
}

// nonNegativeDuration parses an optional Go duration that must not be negative.
func nonNegativeDuration(v, name string) (time.Duration, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return 0, errors.New("olcrtc: " + name + " must be a duration, like 5ms")
	}
	return d, nil
}

// EnsureCryptoKey returns c with a generated key when empty. Call after
// Validate on save so the URI is always complete.
func (c OlcrtcConfig) EnsureCryptoKey() (OlcrtcConfig, error) {
	if strings.TrimSpace(c.CryptoKey) != "" {
		return c, nil
	}
	key, err := GenerateCryptoKey()
	if err != nil {
		return c, err
	}
	c.CryptoKey = key
	return c, nil
}

// ClampVP8 bounds vp8 fps/batch to the ranges the client sanitizes.
func (c OlcrtcConfig) ClampVP8() OlcrtcConfig {
	c.VP8Fps = clampInt(c.VP8Fps, 1, 120)
	c.VP8Batch = clampInt(c.VP8Batch, 1, 64)
	c.SeiFps = clampInt(c.SeiFps, 1, 120)
	c.SeiBatch = clampInt(c.SeiBatch, 1, 64)
	if c.SeiFrag <= 0 {
		c.SeiFrag = 900
	}
	if c.SeiAck <= 0 {
		c.SeiAck = 2000
	}
	c.VideoFps = clampInt(c.VideoFps, 1, 120)
	if c.VideoW <= 0 {
		c.VideoW = 1080
	}
	if c.VideoH <= 0 {
		c.VideoH = 1080
	}
	if c.VideoCodec != "tile" {
		c.VideoCodec = "qrcode"
	}
	if c.VideoCodec == "tile" {
		c.VideoW, c.VideoH = 1080, 1080
	}
	c.AuthToken = strings.TrimSpace(c.AuthToken)
	c.ExtraKeys = cleanKeys(c.ExtraKeys, c.CryptoKey)
	for _, v := range []*string{&c.LivenessInterval, &c.LivenessTimeout, &c.MaxSessionDuration, &c.TrafficMinDelay, &c.TrafficMaxDelay, &c.DTLSProfile, &c.VideoQRRecovery} {
		*v = strings.TrimSpace(*v)
	}
	return c
}

// cleanKeys trims the extra keys, drops blanks, repeats and the main key itself, and lower-cases the rest.
func cleanKeys(keys []string, main string) []string {
	main = strings.ToLower(strings.TrimSpace(main))
	var out []string
	for _, k := range keys {
		k = strings.ToLower(strings.TrimSpace(k))
		if k == "" || k == main || slices.Contains(out, k) {
			continue
		}
		out = append(out, k)
	}
	return out
}

// RenderYAML produces the server YAML the olcrtc binary expects as its sole
// CLI argument. Schema mirrors upstream docs/examples/server/*.yaml and
// Ex3-ui's ServerYAML().
func (c OlcrtcConfig) RenderYAML(dataDir string) string {
	_ = dataDir
	var b strings.Builder
	b.WriteString("mode: srv\n")
	b.WriteString("auth:\n")
	b.WriteString("  provider: " + yamlString(c.Provider) + "\n")
	if tok := strings.TrimSpace(c.AuthToken); tok != "" {
		b.WriteString("  token: " + yamlString(tok) + "\n")
	}
	b.WriteString("room:\n")
	b.WriteString("  id: " + yamlString(c.RoomID) + "\n")
	if key := strings.TrimSpace(c.CryptoKey); key != "" {
		b.WriteString("crypto:\n")
		if extra := cleanKeys(c.ExtraKeys, key); len(extra) > 0 {
			// A ring of keys: the room key and one per person. crypto.key and crypto.keys cannot both be given.
			b.WriteString("  keys:\n")
			for _, k := range append([]string{key}, extra...) {
				b.WriteString("    - " + yamlString(k) + "\n")
			}
		} else {
			b.WriteString("  key: " + yamlString(key) + "\n")
		}
	}
	b.WriteString("net:\n")
	b.WriteString("  transport: " + yamlString(c.Transport) + "\n")
	if dns := strings.TrimSpace(c.DNS); dns != "" {
		b.WriteString("  dns: " + yamlString(dns) + "\n")
	}
	if p := strings.TrimSpace(c.DTLSProfile); p != "" {
		b.WriteString("dtls:\n")
		b.WriteString("  profile: " + yamlString(p) + "\n")
	}
	switch c.Transport {
	case "vp8channel":
		b.WriteString("vp8:\n")
		b.WriteString("  fps: " + strconv.Itoa(c.VP8Fps) + "\n")
		b.WriteString("  batch_size: " + strconv.Itoa(c.VP8Batch) + "\n")
	case "seichannel":
		b.WriteString("sei:\n")
		b.WriteString("  fps: " + strconv.Itoa(c.SeiFps) + "\n")
		b.WriteString("  batch_size: " + strconv.Itoa(c.SeiBatch) + "\n")
		b.WriteString("  fragment_size: " + strconv.Itoa(c.SeiFrag) + "\n")
		b.WriteString("  ack_timeout_ms: " + strconv.Itoa(c.SeiAck) + "\n")
	case "videochannel":
		b.WriteString("video:\n")
		b.WriteString("  width: " + strconv.Itoa(c.VideoW) + "\n")
		b.WriteString("  height: " + strconv.Itoa(c.VideoH) + "\n")
		b.WriteString("  fps: " + strconv.Itoa(c.VideoFps) + "\n")
		b.WriteString("  codec: " + yamlString(c.VideoCodec) + "\n")
		if c.VideoCodec == "tile" {
			if c.VideoTileModule > 0 {
				b.WriteString("  tile_module: " + strconv.Itoa(c.VideoTileModule) + "\n")
			}
			if c.VideoTileRS > 0 {
				b.WriteString("  tile_rs: " + strconv.Itoa(c.VideoTileRS) + "\n")
			}
		} else {
			if c.VideoQRSize > 0 {
				b.WriteString("  qr_size: " + strconv.Itoa(c.VideoQRSize) + "\n")
			}
			if r := strings.TrimSpace(c.VideoQRRecovery); r != "" {
				b.WriteString("  qr_recovery: " + yamlString(r) + "\n")
			}
		}
	}
	// Native SOCKS upstream (upstream olcrtc YAML). When routed, point at the
	// panel-injected Xray loopback SOCKS bridge (noauth).
	if c.RouteThroughXray && c.RouteXrayPort > 0 {
		b.WriteString("socks:\n")
		b.WriteString("  proxy_addr: \"127.0.0.1\"\n")
		b.WriteString("  proxy_port: " + strconv.Itoa(c.RouteXrayPort) + "\n")
		b.WriteString("  proxy_user: \"\"\n")
		b.WriteString("  proxy_pass: \"\"\n")
	}
	if c.UDPEnabled {
		b.WriteString("udp:\n")
		b.WriteString("  enabled: true\n")
		if c.UDPMaxFlows > 0 {
			b.WriteString("  max_flows: " + strconv.Itoa(c.UDPMaxFlows) + "\n")
		}
	}
	b.WriteString("liveness:\n")
	b.WriteString("  interval: " + durationOr(c.LivenessInterval, "10s") + "\n")
	b.WriteString("  timeout: " + durationOr(c.LivenessTimeout, "15s") + "\n")
	failures := 4
	if c.LivenessFailures > 0 {
		failures = c.LivenessFailures
	}
	b.WriteString("  failures: " + strconv.Itoa(failures) + "\n")
	if d := strings.TrimSpace(c.MaxSessionDuration); d != "" {
		b.WriteString("lifecycle:\n")
		b.WriteString("  max_session_duration: " + yamlString(d) + "\n")
	}
	if c.TrafficMaxPayload > 0 || strings.TrimSpace(c.TrafficMinDelay) != "" || strings.TrimSpace(c.TrafficMaxDelay) != "" {
		b.WriteString("traffic:\n")
		if c.TrafficMaxPayload > 0 {
			b.WriteString("  max_payload_size: " + strconv.Itoa(c.TrafficMaxPayload) + "\n")
		}
		if d := strings.TrimSpace(c.TrafficMinDelay); d != "" {
			b.WriteString("  min_delay: " + yamlString(d) + "\n")
		}
		if d := strings.TrimSpace(c.TrafficMaxDelay); d != "" {
			b.WriteString("  max_delay: " + yamlString(d) + "\n")
		}
	}
	b.WriteString("debug: " + strconv.FormatBool(c.Debug) + "\n")
	return b.String()
}

// durationOr returns the trimmed duration, or def when it is empty.
func durationOr(v, def string) string {
	if v = strings.TrimSpace(v); v != "" {
		return v
	}
	return def
}

// ClientURI renders the olcrtc:// connect string imported by olcbox /
// owenclave (grammar from the docs/uri.md of olcrtc and Ex3-ui OlcrtcURI):
//
//	olcrtc://<provider>?<transport>[<k=v&...>]@<room>#<key>[$<name>]
//
// The "$name" tail is the convention's comment field (a client shows it as the
// name of the server); it carries the inbound's remark unless NoLinkName is set.
func (c OlcrtcConfig) ClientURI() string {
	room := strings.TrimSpace(c.RoomID)
	key := strings.TrimSpace(c.CryptoKey)
	if room == "" || key == "" {
		return ""
	}
	transport := strings.TrimSpace(c.Transport)
	switch transport {
	case "vp8channel":
		transport = fmt.Sprintf("vp8channel<vp8-fps=%d&vp8-batch=%d>", c.VP8Fps, c.VP8Batch)
	case "seichannel":
		transport = fmt.Sprintf("seichannel<fps=%d&batch=%d&frag=%d&ack-ms=%d>", c.SeiFps, c.SeiBatch, c.SeiFrag, c.SeiAck)
	case "videochannel":
		codec := c.VideoCodec
		if codec == "" {
			codec = "qrcode"
		}
		params := fmt.Sprintf("video-w=%d&video-h=%d&video-fps=%d&video-codec=%s", c.VideoW, c.VideoH, c.VideoFps, codec)
		if codec == "tile" {
			if c.VideoTileModule > 0 {
				params += fmt.Sprintf("&video-tile-module=%d", c.VideoTileModule)
			}
			if c.VideoTileRS > 0 {
				params += fmt.Sprintf("&video-tile-rs=%d", c.VideoTileRS)
			}
		} else {
			if c.VideoQRSize > 0 {
				params += fmt.Sprintf("&video-qr-size=%d", c.VideoQRSize)
			}
			if r := strings.TrimSpace(c.VideoQRRecovery); r != "" {
				params += "&video-qr-recovery=" + r
			}
		}
		transport = "videochannel<" + params + ">"
	case "":
		transport = "datachannel"
	}
	uri := fmt.Sprintf("olcrtc://%s?%s@%s#%s", strings.TrimSpace(c.Provider), transport, room, key)
	if name := c.linkName(); name != "" {
		uri += "$" + name
	}
	return uri
}

// linkName is the remark as the URI's comment field: one line, without the "$" that starts it, cut to 100 characters.
func (c OlcrtcConfig) linkName() string {
	if c.NoLinkName {
		return ""
	}
	name := strings.Map(func(r rune) rune {
		switch {
		case r == '$':
			return '-'
		case r < 0x20 || r == 0x7f:
			return ' '
		}
		return r
	}, c.Remark)
	name = strings.Join(strings.Fields(name), " ")
	if runes := []rune(name); len(runes) > 100 {
		name = strings.TrimSpace(string(runes[:100]))
	}
	return name
}

// GenerateCryptoKey returns 32 random bytes as a 64-char hex string.
func GenerateCryptoKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func validCryptoKey(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		isDigit := r >= '0' && r <= '9'
		isLower := r >= 'a' && r <= 'f'
		isUpper := r >= 'A' && r <= 'F'
		if !isDigit && !isLower && !isUpper {
			return false
		}
	}
	return true
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// yamlString double-quotes a scalar and escapes embedded quotes/newlines so
// operator input cannot break out of its string token.
func yamlString(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return "\"" + s + "\""
}
