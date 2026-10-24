// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package sub

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/awg/vpnuri"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	wgutil "github.com/mhsanaei/3x-ui/v3/internal/util/wireguard"
)

// getLinkLines is what a subscription carries for one client of one inbound.
func getLinkLines(s *SubService, ib *model.Inbound, email string) []string {
	return splitLinkLines(s.GetLink(ib, email))
}

func amneziaWGNativeInbound(t *testing.T) (*model.Inbound, string) {
	t.Helper()
	serverPriv, serverPub, err := wgutil.GenerateWireguardKeypair()
	if err != nil {
		t.Fatal(err)
	}
	clientPriv, _, err := wgutil.GenerateWireguardKeypair()
	if err != nil {
		t.Fatal(err)
	}
	return &model.Inbound{
		Listen:   "203.0.113.7",
		Port:     51820,
		Protocol: model.AmneziaWG,
		Remark:   "awg-native",
		Settings: `{"server":{"privateKey":"` + serverPriv + `","publicKey":"` + serverPub + `","mtu":1420,"primaryDns":"8.8.8.8"},` +
			`"clients":[{"email":"user","privateKey":"` + clientPriv + `","allowedIPs":["10.8.1.2/32"],"keepAlive":25}]}`,
	}, clientPriv
}

// The same .conf that the INCY line carries, offered to every client as one more line.
func TestAwgSubscriptionCarriesTheConfigLinkForEveryClient(t *testing.T) {
	ib := awgLinkInbound(awgLinkClientSettings)
	lines := getLinkLines(&SubService{}, ib, "user")
	if len(lines) != 3 {
		t.Fatalf("want legacy, vpn:// and amneziawg://<config>, got %d lines:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	if !strings.HasPrefix(lines[0], "amneziawg://") || !strings.Contains(lines[0], "@") || !strings.HasPrefix(lines[1], "vpn://") {
		t.Fatalf("the first two lines must stay the legacy link and vpn://:\n%s\n%s", lines[0], lines[1])
	}
	conf, name := incyAwgLine(t, lines[2])

	incy := &SubService{}
	incy.incyAwgExport = true
	incyLines := getLinkLines(incy, ib, "user")
	if len(incyLines) != 1 {
		t.Fatalf("INCY must keep getting one line, got %d", len(incyLines))
	}
	wantConf, wantName := incyAwgLine(t, incyLines[0])
	if conf != wantConf || name != wantName {
		t.Errorf("the config link differs from the INCY one:\n got %q / %q\nwant %q / %q", conf, name, wantConf, wantName)
	}
	// and the vpn:// line carries the very same config
	payload, err := vpnuri.Decode(lines[1])
	if err != nil {
		t.Fatal(err)
	}
	if fromVpn, err := vpnuri.ConfFromPayload(payload); err != nil || strings.TrimSpace(fromVpn) != strings.TrimSpace(conf) || !strings.HasSuffix(conf, "\n") {
		t.Errorf("vpn:// and amneziawg:// carry different configs (%v):\n%s\n---\n%s", err, fromVpn, conf)
	}
}

func TestIncyStillGetsOnlyTheConfigLineForAwg(t *testing.T) {
	s := &SubService{}
	s.incyAwgExport = true
	lines := getLinkLines(s, awgLinkInbound(awgLinkClientSettings), "user")
	if len(lines) != 1 || strings.Contains(lines[0], "vpn://") {
		t.Fatalf("lines = %v", lines)
	}
}

func TestEveryHostGetsItsOwnConfigLinkNextToItsVpnLine(t *testing.T) {
	ib := awgLinkInbound(awgLinkClientSettings)
	ib.StreamSettings = `{"externalProxy":[` +
		`{"dest":"test.com","port":443,"remark":"cdn","isHost":true,"remarkFinal":true},` +
		`{"dest":"direct.test.net","port":51820,"remark":"direct","isHost":true,"remarkFinal":true}]}`
	lines := getLinkLines(&SubService{}, ib, "user")
	if len(lines) != 6 {
		t.Fatalf("two hosts must give three lines each, got %d:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	for i, want := range []struct{ name, endpoint string }{{"cdn", "Endpoint = test.com:443"}, {"direct", "Endpoint = direct.test.net:51820"}} {
		legacy, vpn, conf := lines[i*3], lines[i*3+1], lines[i*3+2]
		if !strings.Contains(legacy, "@") || !strings.HasPrefix(vpn, "vpn://") {
			t.Fatalf("host %d: the order must be legacy, vpn://, amneziawg://<config>:\n%s", i, strings.Join(lines, "\n"))
		}
		text, name := incyAwgLine(t, conf)
		if name != want.name || !strings.Contains(text, want.endpoint) {
			t.Errorf("host %d: name %q, config:\n%s", i, name, text)
		}
	}
}

func TestAmneziaWGInboundsCarryTheConfigLinkToo(t *testing.T) {
	ib, clientPriv := amneziaWGNativeInbound(t)
	for _, incy := range []bool{false, true} {
		s := &SubService{}
		s.incyAwgExport = incy
		lines := getLinkLines(s, ib, "user")
		if len(lines) != 2 || !strings.HasPrefix(lines[0], "vpn://") {
			t.Fatalf("incy=%v: want vpn:// and amneziawg://<config>, got:\n%s", incy, strings.Join(lines, "\n"))
		}
		conf, name := incyAwgLine(t, lines[1])
		if !strings.Contains(conf, "PrivateKey = "+clientPriv) || !strings.Contains(conf, "Endpoint = 203.0.113.7:51820") {
			t.Errorf("incy=%v: config:\n%s", incy, conf)
		}
		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(lines[0], "vpn://"))
		if err != nil || string(raw) != conf {
			t.Errorf("incy=%v: vpn:// and amneziawg:// must carry the same text (%v)", incy, err)
		}
		if name != awgConfName(conf) {
			t.Errorf("incy=%v: the link name %q is not the config's comment %q", incy, name, awgConfName(conf))
		}
	}
}

func TestOtherProtocolsAndEmptyLinksAreUntouched(t *testing.T) {
	s := &SubService{}
	for in, want := range map[string]string{
		"":                                      "",
		"vless://u@h:1#x":                       "vless://u@h:1#x",
		"vless://u@h:1#x\nvpn://not-a-vpn-link": "vless://u@h:1#x\nvpn://not-a-vpn-link",
	} {
		if got := s.withAwgConfLinks(in); got != want {
			t.Errorf("withAwgConfLinks(%q) = %q, want %q", in, got, want)
		}
	}
	ib := awgLinkInbound(awgLinkClientSettings)
	ib.Enable = false
	if got := s.GetLink(ib, "user"); got != "" && !strings.Contains(got, "amneziawg://") {
		t.Errorf("a disabled inbound gave %q", got)
	}
}

func TestAConfigWithoutANameGivesALinkWithoutAFragment(t *testing.T) {
	conf := "[Interface]\nPrivateKey = k\nAddress = 10.0.0.2/32\n[Peer]\nPublicKey = p\nEndpoint = h:1\n"
	link := awgConfLink(conf)
	if strings.Contains(link, "#") || !strings.HasPrefix(link, "amneziawg://") {
		t.Errorf("link = %q", link)
	}
	named := awgConfLink("# My node 1\n" + conf)
	if !strings.HasSuffix(named, "#My%20node%201") {
		t.Errorf("link = %q", named)
	}
	// an empty comment line does not hide a later, named one
	if got := awgConfName("# \n[Interface]\nPrivateKey = k\n# Named\n[Peer]\n"); got != "Named" {
		t.Errorf("name = %q", got)
	}
	// the AmneziaWG module puts its comment before [Peer]
	beforePeer := awgConfLink("[Interface]\nPrivateKey = k\n\n# Poland | VK\n[Peer]\nPublicKey = p\n")
	if !strings.HasSuffix(beforePeer, "#Poland%20%7C%20VK") {
		t.Errorf("link = %q", beforePeer)
	}
}

func TestAConfigIsFoundInBothKindsOfVpnLink(t *testing.T) {
	conf := "# n\n[Interface]\nPrivateKey = k\nAddress = 10.0.0.2/32\n[Peer]\nPublicKey = p\nEndpoint = h:1\n"
	plain := "vpn://" + base64.RawURLEncoding.EncodeToString([]byte(conf))
	if got := awgConfFromVpn(plain); got != conf {
		t.Errorf("plain: %q", got)
	}
	packed, err := vpnuri.EncodeConf(conf)
	if err != nil {
		t.Fatal(err)
	}
	if got := awgConfFromVpn(packed); strings.TrimSpace(got) != strings.TrimSpace(conf) {
		t.Errorf("packed: %q", got)
	}
	for _, bad := range []string{
		"vpn://", "vpn://!!!",
		"vpn://" + base64.RawURLEncoding.EncodeToString([]byte("hello")),
		"vpn://" + base64.RawURLEncoding.EncodeToString([]byte("[Interface]\nPrivateKey = k\n")),
		"vpn://" + base64.RawURLEncoding.EncodeToString([]byte("[Peer]\nPublicKey = p\n")),
	} {
		if got := awgConfFromVpn(bad); got != "" {
			t.Errorf("%q gave %q", bad, got)
		}
	}
}
