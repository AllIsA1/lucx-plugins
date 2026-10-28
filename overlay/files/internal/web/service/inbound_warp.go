// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package service

import (
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
)

// A WARP inbound runs nothing and listens on nothing. The row holds the settings of the WARP nodes the subscription
// gives its clients; the Better WARP plugin registers the devices and writes the lines. The settings are checked here
// so that what the plugin reads is always something it can use. The checks are the plugin's own, kept in step by hand.

var (
	warpHostPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	warpIPattern    = regexp.MustCompile(`^[ -~]{0,1000}$`)
	warpPresets     = map[string][3]int{"standard": {4, 40, 70}, "light": {2, 20, 40}, "strong": {8, 50, 150}}
)

const warpMaxEndpoints = 3

func warpValidHostPort(s string) bool {
	host, port, err := net.SplitHostPort(strings.TrimSpace(s))
	if err != nil {
		return false
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}
	return warpHostPattern.MatchString(host)
}

func warpInt(m map[string]any, key string, def int) int {
	switch v := m[key].(type) {
	case int:
		return v
	case float64:
		return int(v)
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

// normalizeWarpSettings checks a WARP inbound's settings, fills in what is missing and makes the row portless.
func (s *InboundService) normalizeWarpSettings(inbound *model.Inbound) error {
	if inbound.Protocol != model.Warp {
		return nil
	}
	inbound.Port = 0
	settings := map[string]any{}
	if strings.TrimSpace(inbound.Settings) != "" {
		if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
			return common.NewError("warp settings are not valid JSON")
		}
	}
	// The node's name in a subscription is the inbound's remark; an older row may still carry a name of its own.
	delete(settings, "name")

	// Endpoints: none means "the ones Better WARP keeps" (its scan), so the list may be empty.
	var endpoints []string
	if raw, ok := settings["endpoints"].([]any); ok {
		for _, e := range raw {
			text, _ := e.(string)
			text = strings.TrimSpace(text)
			if text == "" {
				continue
			}
			if !warpValidHostPort(text) {
				return common.NewErrorf("warp: endpoint %q must be host:port", text)
			}
			endpoints = append(endpoints, text)
		}
	}
	if len(endpoints) > warpMaxEndpoints {
		return common.NewErrorf("warp: at most %d endpoints", warpMaxEndpoints)
	}
	if endpoints == nil {
		endpoints = []string{}
	}
	settings["endpoints"] = endpoints

	mtu := warpInt(settings, "mtu", 1280)
	if mtu < 576 || mtu > 1420 {
		return common.NewError("warp: MTU must be between 576 and 1420")
	}
	settings["mtu"] = mtu
	if ka := warpInt(settings, "keepalive", 25); ka < 0 || ka > 600 {
		return common.NewError("warp: keepalive is 0 to 600 seconds")
	} else {
		settings["keepalive"] = ka
	}
	dns, _ := settings["dns"].(string)
	dns = strings.TrimSpace(dns)
	if dns == "" {
		dns = "1.1.1.1, 1.0.0.1, 2606:4700:4700::1111, 2606:4700:4700::1001"
	}
	for _, part := range strings.Split(dns, ",") {
		if _, err := netip.ParseAddr(strings.TrimSpace(part)); err != nil {
			return common.NewErrorf("warp: DNS server %q is not an IP address", strings.TrimSpace(part))
		}
	}
	settings["dns"] = dns
	if _, ok := settings["ipv6"].(bool); !ok {
		settings["ipv6"] = true
	}

	preset, _ := settings["preset"].(string)
	awg, _ := settings["awg"].(map[string]any)
	if awg == nil {
		awg = map[string]any{}
	}
	switch p, ok := warpPresets[preset]; {
	case ok:
		awg["jc"], awg["jmin"], awg["jmax"], awg["s1"], awg["s2"] = p[0], p[1], p[2], 0, 0
	case preset == "custom" || preset == "":
		preset = "custom"
		if _, has := awg["jc"]; !has {
			awg["jc"], awg["jmin"], awg["jmax"] = 4, 40, 70
		}
	default:
		return common.NewError("warp: preset must be standard, light, strong or custom")
	}
	settings["preset"] = preset
	jc, jmin, jmax := warpInt(awg, "jc", 4), warpInt(awg, "jmin", 40), warpInt(awg, "jmax", 70)
	if jc < 0 || jc > 16 || jmin < 0 || jmax < jmin || jmax > 1280 {
		return common.NewError("warp: junk packets are Jc 0-16 and 0 <= Jmin <= Jmax <= 1280")
	}
	s1, s2 := warpInt(awg, "s1", 0), warpInt(awg, "s2", 0)
	if s1 < 0 || s1 > 1132 || s2 < 0 || s2 > 1188 {
		return common.NewError("warp: S1 and S2 are padding sizes (0 for WARP)")
	}
	awg["jc"], awg["jmin"], awg["jmax"], awg["s1"], awg["s2"] = jc, jmin, jmax, s1, s2
	for _, k := range []string{"h1", "h2", "h3", "h4", "i1", "i2", "i3", "i4", "i5"} {
		v, _ := awg[k].(string)
		v = strings.TrimSpace(v)
		if v != "" && !warpIPattern.MatchString(v) {
			return fmt.Errorf("warp: %s may contain printable ASCII only", strings.ToUpper(k))
		}
		if v == "" {
			delete(awg, k)
		} else {
			awg[k] = v
		}
	}
	settings["awg"] = awg

	out, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	inbound.Settings = string(out)
	return nil
}
