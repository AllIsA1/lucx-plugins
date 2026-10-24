// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package tunnel

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"
)

// An OpenFlux exit in l3 mode forwards raw packets. The kernel sees replies for connections it never opened
// and answers them with RSTs, which tear the tunnel's connections down, so the host must drop the RSTs it
// makes itself (OpenFlux docs, "l3 and kernel RSTs"). The rule covers the host's own address, which also
// hides the RSTs of its closed ports; the panel installs it only while an l3 OpenFlux inbound is enabled
// and removes it when none is, and when the panel stops.

var (
	openfluxFirewallMu sync.Mutex
	openfluxRSTIP      string // the address the installed rule names, "" when there is none

	// Replaced by tests.
	openfluxEgressIP = detectOutboundIPv4
	openfluxIptables = func(args ...string) error {
		return exec.CommandContext(context.Background(), "iptables", args...).Run()
	}
)

func openfluxRSTSpec(ip string) []string {
	return []string{"OUTPUT", "-p", "tcp", "--tcp-flags", "RST", "RST", "-s", ip, "-j", "DROP"}
}

// openfluxWantsL3 says whether a desired instance is an enabled exit in l3 mode.
func openfluxWantsL3(want []Instance) bool {
	for _, in := range want {
		if in.Enabled && strings.Contains(in.ConfigText, "\nMode = "+OpenfluxModeL3+"\n") {
			return true
		}
	}
	return false
}

// syncOpenfluxFirewall brings the RST drop rule in line with the instances that are wanted.
func syncOpenfluxFirewall(want []Instance) {
	if runtime.GOOS != "linux" {
		return
	}
	openfluxFirewallMu.Lock()
	defer openfluxFirewallMu.Unlock()
	if !openfluxWantsL3(want) {
		removeOpenfluxRSTLocked()
		return
	}
	ip := openfluxEgressIP()
	if ip == "" {
		logger.Warning("tunnel: openflux l3: the host's address is unknown, the RST drop rule is not installed")
		return
	}
	if openfluxRSTIP != "" && openfluxRSTIP != ip {
		removeOpenfluxRSTLocked()
	}
	spec := openfluxRSTSpec(ip)
	if openfluxIptables(append([]string{"-C"}, spec...)...) == nil {
		openfluxRSTIP = ip
		return
	}
	if err := openfluxIptables(append([]string{"-A"}, spec...)...); err != nil {
		logger.Warningf("tunnel: openflux l3: cannot install the RST drop rule: %v", err)
		return
	}
	openfluxRSTIP = ip
}

// clearOpenfluxFirewall removes the rule (the panel is stopping).
func clearOpenfluxFirewall() {
	if runtime.GOOS != "linux" {
		return
	}
	openfluxFirewallMu.Lock()
	defer openfluxFirewallMu.Unlock()
	removeOpenfluxRSTLocked()
}

func removeOpenfluxRSTLocked() {
	if openfluxRSTIP == "" {
		return
	}
	spec := openfluxRSTSpec(openfluxRSTIP)
	for i := 0; i < 4; i++ { // a rule that was added twice
		if openfluxIptables(append([]string{"-D"}, spec...)...) != nil {
			break
		}
	}
	openfluxRSTIP = ""
}
