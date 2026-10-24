// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package service

import (
	"strings"
	"testing"
)

func TestAnyShareLinkOfAnySchemeIsAcceptedAsAnExternalLink(t *testing.T) {
	for _, link := range []string{
		"vless://u@host.example:443?type=tcp#one",
		"vmess://eyJwcyI6IngifQ==",
		"amneziawg://IyDwn4e18J-HsSDQn9C-0LvRjNGI0LAgfCAxINCT0LHQuNGCL9GBCltJbnRlcmZhY2VdClByaXZhdGVLZXk",
		"yptun://inbound?v=2&c=eNqVUE1Kw0AUvkqYdfP_0yYgIhW6ciEoFcXFJDNpB6czYTKpSWMWunIhFE9Q9ASCCILgGdKT6BGcYBGLK3eP9_2-VwMGZxhE4HO1fP54XWrtffugtav2vX1b361v2yftSmsf25f19foG9ABmE8I6Ps8wS2lRqt3PGNUgx4nAUuGhnyIHO56DbN-Nkef0PT8O_DR2oYMC21G6hDOJy448lTLLI9NEJL8wKsgQLg1RmMRcDLPxib8f6ofDkb6nNAxPuCRQqgpSFFiZQCEIFjmIzmogq6zrhojAiVTsTBAuiKxAZFtWT-0hVXDGqYowIF2UBmdU3RPZA8_zQdOrQSHoPwp1ik3oDBIqChW67ZBQXiCjAzuDrIgpSczFaHZsTqvTo9MgHR9sFfWsX55JkeXfDf_4EvU7MSf40uhImztMSuZYTzgibGLuCs5nOzANHBgmA30QIEf3LIj1OAxt3YXI6kPbhmrYKuBazXnTfAGDgLs3",
		"qwdtt://config?hashes=AaLB%2CV4_y&name=%F0%9F%8F%B3&port=9000",
		"openflux://v1/XM1BioMwFIf",
		"anytls://pass@host.example:443/?sni=x",
		"future+scheme://whatever/at/all",
	} {
		if err := validateExternalShareLink(link); err != nil {
			t.Errorf("%q was refused: %v", link[:min(len(link), 40)], err)
		}
	}
}

func TestAnExternalLinkThatCouldBreakTheSubscriptionIsRefused(t *testing.T) {
	for name, link := range map[string]string{
		"no scheme":           "example.com/path",
		"empty scheme part":   "://host",
		"nothing after":       "vless://",
		"a space inside":      "vless://a b",
		"a second line":       "vless://a\nopenflux://b",
		"a carriage return":   "vless://a\rb",
		"a tab":               "vless://a\tb",
		"a control character": "vless://a\x07b",
		"a null":              "vless://a\x00b",
		"a delete character":  "vless://a\x7fb",
		"a space in scheme":   "my scheme://x",
		"digit first":         "1vless://x",
		"too long":            "vless://" + strings.Repeat("a", externalLinkMaxLength),
		"plain words":         "hello world",
	} {
		if err := validateExternalShareLink(link); err == nil {
			t.Errorf("%s: %q was accepted", name, link[:min(len(link), 40)])
		}
	}
}

func TestTheLongestAllowedLinkIsAccepted(t *testing.T) {
	link := "vless://" + strings.Repeat("a", externalLinkMaxLength-len("vless://"))
	if err := validateExternalShareLink(link); err != nil {
		t.Errorf("a link of exactly %d bytes was refused: %v", len(link), err)
	}
}

func TestOneByteOverTheLimitIsRefused(t *testing.T) {
	link := "vless://" + strings.Repeat("a", externalLinkMaxLength-len("vless://")+1)
	if err := validateExternalShareLink(link); err == nil {
		t.Errorf("a link of %d bytes was accepted", len(link))
	}
}

func TestSurroundingBlanksAreIgnored(t *testing.T) {
	if err := validateExternalShareLink("  vless://a#b \n"); err != nil {
		t.Errorf("blanks around a link were refused: %v", err)
	}
}

func TestTheClientFormTakesLinksOfAnySchemeAndStillRefusesBrokenOnes(t *testing.T) {
	rows, err := normalizeExternalLinks([]ExternalLinkInput{
		{Kind: "link", Value: " amneziawg://IyDwn4e18J-HsSDQ ", Remark: "AWG"},
		{Kind: "link", Value: "yptun://inbound?v=2"},
		{Kind: "", Value: "vless://u@h:1#ok"},
		{Kind: "link", Value: ""},
	})
	if err != nil || len(rows) != 3 || rows[0].Value != "amneziawg://IyDwn4e18J-HsSDQ" || rows[0].Remark != "AWG" || rows[1].Value != "yptun://inbound?v=2" {
		t.Fatalf("rows = %+v, err = %v", rows, err)
	}
	for _, bad := range []string{"vless://a b", "vless://a\nb", "not a link", ""} {
		if bad == "" {
			continue
		}
		if _, err := normalizeExternalLinks([]ExternalLinkInput{{Kind: "link", Value: bad}}); err == nil {
			t.Errorf("%q was accepted by the client form", bad)
		}
	}
	if _, err := normalizeExternalLinks([]ExternalLinkInput{{Kind: "subscription", Value: "yptun://x"}}); err == nil {
		t.Error("an external subscription must still be an http(s) URL")
	}
}
