// Copyright (c) 2025 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package tunnel

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

func TestOpenfluxConfAndLink(t *testing.T) {
	cfg := OpenfluxConfig{
		Secret:    "0123456789abcdef",
		YandexURL: "https://docs.example/view?id=1",
	}
	if err := cfg.Validate(18445); err != nil {
		t.Fatal(err)
	}
	conf := cfg.RenderConf("openflux-3", 18445)
	if !strings.Contains(conf, "Mode = l4") || !strings.Contains(conf, "Listen = 0.0.0.0:18445") {
		t.Fatalf("conf missing l4 listen:\n%s", conf)
	}
	if !strings.Contains(conf, "Type = yandex") || strings.Contains(conf, "Mode = l3") {
		t.Fatalf("conf shape:\n%s", conf)
	}
	link, err := cfg.ClientURI("exit", "203.0.113.5", 18445)
	if err != nil || !strings.HasPrefix(link, openfluxLinkPrefix) {
		t.Fatalf("link %q err %v", link, err)
	}
	body, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(link, openfluxLinkPrefix))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(flate.NewReader(bytes.NewReader(body)))
	if err != nil {
		t.Fatal(err)
	}
	var got openfluxShare
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Negotiate || got.Secret != cfg.Secret || got.Context != cfg.YandexURL {
		t.Fatalf("payload %+v", got)
	}
	if len(got.Transports) != 2 || got.Transports[0].Dial != "203.0.113.5:18445" {
		t.Fatalf("transports %+v", got.Transports)
	}
}

func TestOpenfluxStoredBlockDecodes(t *testing.T) {
	raw := []byte(`{"negotiate":true,"secret":"0123456789abcdef","context":"http://openflux","transports":[{"type":"direct","priority":100,"dial":"1.2.3.4:18445"}]}`)
	packed := openfluxStoredDeflate(raw)
	got, err := io.ReadAll(flate.NewReader(bytes.NewReader(packed)))
	if err != nil || string(got) != string(raw) {
		t.Fatalf("stored block %q err %v", got, err)
	}
}

func openfluxStoredDeflate(data []byte) []byte {
	var out []byte
	for offset := 0; offset < len(data) || offset == 0; {
		n := len(data) - offset
		if n > 65535 {
			n = 65535
		}
		final := byte(0)
		if offset+n >= len(data) {
			final = 1
		}
		nlen := n ^ 0xffff
		out = append(out, final, byte(n), byte(n>>8), byte(nlen), byte(nlen>>8))
		out = append(out, data[offset:offset+n]...)
		offset += n
		if len(data) == 0 {
			break
		}
	}
	return out
}

func TestOpenfluxInstanceDisabledWithoutSecret(t *testing.T) {
	ib := &model.Inbound{Id: 4, Protocol: model.Openflux, Enable: true, Port: 18445, Settings: `{}`}
	inst, ok := OpenfluxInstanceFromInbound(ib)
	if !ok || inst.Enabled || inst.Key != "openflux-4" {
		t.Fatalf("empty secret must not start: %+v ok=%v", inst, ok)
	}
}

func TestOpenfluxRejectsHashInURL(t *testing.T) {
	cfg := OpenfluxConfig{Secret: "0123456789abcdef", YandexURL: "https://docs.example/a#b"}
	if err := cfg.Validate(18445); err == nil {
		t.Fatal("hash in URL must be rejected (conf parser cuts at #)")
	}
}

func openfluxDecodeLink(t *testing.T, link string) openfluxShare {
	t.Helper()
	body, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(link, openfluxLinkPrefix))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(flate.NewReader(bytes.NewReader(body)))
	if err != nil {
		t.Fatal(err)
	}
	var got openfluxShare
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestOpenfluxNewCarriersGoToTheConfAndTheLink(t *testing.T) {
	cfg := OpenfluxConfig{
		Secret:      "0123456789abcdef",
		YandexURL:   "https://docs.example/view?id=1",
		VolgaURL:    "https://volga.example/a",
		BoardURL:    "https://board.example/b",
		AvitoURL:    "https://avito.example/c",
		TelemostURL: "https://telemost.yandex.ru/j/1",
	}.Merge()
	if err := cfg.Validate(18445); err != nil {
		t.Fatal(err)
	}
	conf := cfg.RenderConf("openflux-3", 18445)
	for _, want := range []string{
		`[Transport "vyandex"]` + "\nType = vyandex\nPriority = 48\nURL = https://volga.example/a",
		`[Transport "boards"]` + "\nType = boards\nPriority = 46\nURL = https://board.example/b",
		`[Transport "telemost"]` + "\nType = telemost\nPriority = 44\nURL = https://telemost.yandex.ru/j/1",
		`[Transport "avito"]` + "\nType = avito\nPriority = 35\nURL = https://avito.example/c",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("conf lacks %q:\n%s", want, conf)
		}
	}
	link, err := cfg.ClientURI("exit", "203.0.113.5", 18445)
	if err != nil {
		t.Fatal(err)
	}
	got := openfluxDecodeLink(t, link)
	types := map[string]string{}
	for _, tr := range got.Transports {
		types[tr.Type] = tr.URL
	}
	for typ, url := range map[string]string{"yandex": cfg.YandexURL, "vyandex": cfg.VolgaURL, "boards": cfg.BoardURL, "telemost": cfg.TelemostURL, "avito": cfg.AvitoURL} {
		if types[typ] != url {
			t.Errorf("link transport %s = %q, want %q (all: %+v)", typ, types[typ], url, got.Transports)
		}
	}
	if len(got.Transports) != 6 || got.Transports[0].Type != "direct" {
		t.Errorf("transports = %+v, want direct and five carriers", got.Transports)
	}
}

func TestOpenfluxSeveralLinksInOneFieldAreOneGroup(t *testing.T) {
	cfg := OpenfluxConfig{
		Secret:    "0123456789abcdef",
		MailruURL: " https://cloud.mail.ru/public/a/b \n https://cloud.mail.ru/public/c/d|https://cloud.mail.ru/public/e/f ",
	}.Merge()
	want := "https://cloud.mail.ru/public/a/b,https://cloud.mail.ru/public/c/d,https://cloud.mail.ru/public/e/f"
	if cfg.MailruURL != want {
		t.Fatalf("MailruURL = %q, want %q", cfg.MailruURL, want)
	}
	if err := cfg.Validate(18445); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfg.RenderConf("openflux-1", 18445), "URL = "+want+"\n") {
		t.Error("the conf must carry the links as one comma-separated value")
	}
	if cfg.contextURL() != want {
		t.Errorf("context = %q", cfg.contextURL())
	}

	bad := cfg
	bad.MailruURL += ",ftp://example.com/x"
	if err := bad.Validate(18445); err == nil {
		t.Error("a link that is not http(s) must be refused wherever it stands in the field")
	}
	many := cfg
	many.MailruURL = strings.Repeat("https://cloud.mail.ru/public/a/b,", openfluxMaxLinks+1)
	if err := many.Validate(18445); err == nil {
		t.Error("more links than the limit must be refused")
	}
}

func TestOpenfluxExitModeDefaultsToL4AndAcceptsL3(t *testing.T) {
	old := OpenfluxConfig{Secret: "0123456789abcdef", YandexURL: "https://docs.example/view?id=1"}.Merge()
	if old.ExitMode != OpenfluxModeL4 || old.UsesL3() || !strings.Contains(old.RenderConf("openflux-1", 1), "Mode = l4\n") {
		t.Errorf("an inbound saved before the mode existed must stay l4: %+v", old)
	}
	l3 := OpenfluxConfig{Secret: "0123456789abcdef", ExitMode: " L3 "}.Merge()
	if err := l3.Validate(18445); err != nil {
		t.Fatal(err)
	}
	conf := l3.RenderConf("openflux-1", 18445)
	if !l3.UsesL3() || !strings.Contains(conf, "Mode = l3\n") || strings.Contains(conf, "Mode = l4") {
		t.Errorf("l3 conf:\n%s", conf)
	}
	if err := (OpenfluxConfig{Secret: "0123456789abcdef", ExitMode: "l5"}).Validate(18445); err == nil {
		t.Error("an unknown exit mode must be refused")
	}
}

func TestOpenfluxContextKeepsTheOldOrderAndFindsTheNewFields(t *testing.T) {
	both := OpenfluxConfig{YandexURL: "https://y.example/1", VolgaURL: "https://v.example/1"}.Merge()
	if both.contextURL() != "https://y.example/1" {
		t.Errorf("an existing yandex-based inbound must keep its context: %q", both.contextURL())
	}
	if got := (OpenfluxConfig{AvitoURL: "https://a.example/1"}).Merge().contextURL(); got != "https://a.example/1" {
		t.Errorf("context = %q, want the first link there is", got)
	}
}

type fakeIptables struct {
	rules map[string]int
	calls []string
}

func (f *fakeIptables) run(args ...string) error {
	f.calls = append(f.calls, strings.Join(args, " "))
	key := strings.Join(args[1:], " ")
	switch args[0] {
	case "-C":
		if f.rules[key] > 0 {
			return nil
		}
		return io.ErrUnexpectedEOF
	case "-A":
		f.rules[key]++
	case "-D":
		if f.rules[key] == 0 {
			return io.ErrUnexpectedEOF
		}
		f.rules[key]--
	}
	return nil
}

func withFakeFirewall(t *testing.T, ip string) *fakeIptables {
	t.Helper()
	f := &fakeIptables{rules: map[string]int{}}
	oldIP, oldRun, oldHeld := openfluxEgressIP, openfluxIptables, openfluxRSTIP
	openfluxEgressIP, openfluxIptables, openfluxRSTIP = func() string { return ip }, f.run, ""
	t.Cleanup(func() { openfluxEgressIP, openfluxIptables, openfluxRSTIP = oldIP, oldRun, oldHeld })
	return f
}

func openfluxInstance(t *testing.T, mode string) Instance {
	t.Helper()
	cfg := OpenfluxConfig{Secret: "0123456789abcdef", ExitMode: mode}.Merge()
	return Instance{Core: Openflux, Key: "openflux-1", Enabled: true, ConfigText: cfg.RenderConf("openflux-1", 18445)}
}

func TestOpenfluxL3InstallsTheRSTDropOnceAndRemovesItWhenNoL3IsLeft(t *testing.T) {
	f := withFakeFirewall(t, "203.0.113.5")
	rule := "OUTPUT -p tcp --tcp-flags RST RST -s 203.0.113.5 -j DROP"

	syncOpenfluxFirewall([]Instance{openfluxInstance(t, "l4")})
	if len(f.rules) != 0 {
		t.Fatalf("an l4 exit must not touch the firewall: %v", f.calls)
	}
	syncOpenfluxFirewall([]Instance{openfluxInstance(t, "l3")})
	syncOpenfluxFirewall([]Instance{openfluxInstance(t, "l3")})
	if f.rules[rule] != 1 {
		t.Fatalf("rules = %v, want the rule exactly once", f.rules)
	}
	off := openfluxInstance(t, "l3")
	off.Enabled = false
	syncOpenfluxFirewall([]Instance{off, openfluxInstance(t, "l4")})
	if f.rules[rule] != 0 {
		t.Errorf("rules = %v, want the rule gone when no enabled l3 exit is left", f.rules)
	}
}

func TestOpenfluxL3RuleFollowsTheHostAddressAndGoesOnShutdown(t *testing.T) {
	f := withFakeFirewall(t, "203.0.113.5")
	syncOpenfluxFirewall([]Instance{openfluxInstance(t, "l3")})
	openfluxEgressIP = func() string { return "198.51.100.7" }
	syncOpenfluxFirewall([]Instance{openfluxInstance(t, "l3")})
	if f.rules["OUTPUT -p tcp --tcp-flags RST RST -s 203.0.113.5 -j DROP"] != 0 || f.rules["OUTPUT -p tcp --tcp-flags RST RST -s 198.51.100.7 -j DROP"] != 1 {
		t.Errorf("rules = %v, want only the rule of the new address", f.rules)
	}
	clearOpenfluxFirewall()
	for r, n := range f.rules {
		if n != 0 {
			t.Errorf("rule %q left after shutdown", r)
		}
	}
}

func TestOpenfluxL3WithoutAHostAddressInstallsNothing(t *testing.T) {
	f := withFakeFirewall(t, "")
	syncOpenfluxFirewall([]Instance{openfluxInstance(t, "l3")})
	if len(f.calls) != 0 {
		t.Errorf("calls = %v", f.calls)
	}
}

func TestOpenfluxDebugLevelIsOffByDefaultAndMakesTheCoreLogSessions(t *testing.T) {
	plain := OpenfluxConfig{Secret: "0123456789abcdef"}.Merge()
	if strings.Contains(plain.RenderConf("openflux-1", 18445), "Debug") {
		t.Error("an exit that was not asked for debug output must log as before")
	}
	for _, level := range []int{1, 2, 3} {
		cfg := OpenfluxConfig{Secret: "0123456789abcdef", DebugLevel: level}.Merge()
		conf := cfg.RenderConf("openflux-1", 18445)
		if cfg.DebugLevel != 2 || !strings.Contains(conf, "\nDebug = 2\n") {
			t.Errorf("level %d: %d, conf:\n%s", level, cfg.DebugLevel, conf)
		}
		if strings.Index(conf, "Debug = 2") > strings.Index(conf, "[Transport") {
			t.Error("Debug belongs to [Interface], before the transports")
		}
	}
	if (OpenfluxConfig{DebugLevel: -4}).Merge().DebugLevel != 0 {
		t.Error("a negative level must mean off")
	}
	// a restart is the way a changed level takes effect: it changes the conf, so the fingerprint
	a := OpenfluxConfig{Secret: "0123456789abcdef"}.Merge().RenderConf("openflux-1", 18445)
	b := OpenfluxConfig{Secret: "0123456789abcdef", DebugLevel: 2}.Merge().RenderConf("openflux-1", 18445)
	if a == b {
		t.Error("the conf did not change with the level")
	}
}
