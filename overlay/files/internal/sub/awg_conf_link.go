// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package sub

import (
	"encoding/base64"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/awg/vpnuri"
)

// withAwgConfLinks adds, after every vpn:// line of an AmneziaWG share link, the same .conf as an
// amneziawg://<base64url conf>#name line: the form INCY imports (see genAwgLink) offered to every client.
// The panel builds the .conf once and wraps it into vpn://, so the config is read back from that line:
// the two lines carry the same text whatever way the .conf was assembled. A vpn:// line that holds no
// .conf is left as it is.
func (s *SubService) withAwgConfLinks(link string) string {
	if !strings.Contains(link, "vpn://") {
		return link
	}
	lines := splitLinkLines(link)
	out := make([]string, 0, len(lines)+len(lines)/2)
	for _, line := range lines {
		out = append(out, line)
		if !strings.HasPrefix(line, "vpn://") {
			continue
		}
		if conf := awgConfFromVpn(line); conf != "" {
			out = append(out, awgConfLink(conf))
		}
	}
	return strings.Join(out, "\n")
}

// awgConfLink is amneziawg://<base64url .conf>#<name>, the name being the "# name" first line of the conf.
func awgConfLink(conf string) string {
	link := "amneziawg://" + base64.RawURLEncoding.EncodeToString([]byte(conf))
	if name := awgConfName(conf); name != "" {
		link += "#" + escapeLinkFragment(name, encodeURIComponent)
	}
	return link
}

// awgConfName is the remark stamped into a share config as a "# remark" comment line: the first line when
// it is the stamp of the AWG links, or the one before [Peer] in the AmneziaWG module's config.
func awgConfName(conf string) string {
	for _, line := range strings.Split(conf, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "#"); ok {
			if name := strings.TrimSpace(rest); name != "" {
				return name
			}
		}
	}
	return ""
}

// awgConfFromVpn reads the .conf out of a vpn:// line: the AmneziaWG module's own links carry the text as
// base64url, the AWG links wrap it in Amnezia's compressed container. "" when the line holds neither.
func awgConfFromVpn(line string) string {
	body := strings.TrimPrefix(strings.TrimSpace(line), "vpn://")
	if raw, err := base64.RawURLEncoding.DecodeString(body); err == nil {
		if text := string(raw); strings.Contains(text, "[Interface]") && strings.Contains(text, "[Peer]") {
			return text
		}
	}
	payload, err := vpnuri.Decode(line)
	if err != nil {
		return ""
	}
	conf, err := vpnuri.ConfFromPayload(payload)
	if err != nil {
		return ""
	}
	// the container hands the text back without its last line break; INCY's own line keeps it
	return strings.TrimRight(conf, "\n") + "\n"
}
