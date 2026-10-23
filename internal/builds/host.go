// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package builds

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"time"
)

const (
	// HostSchemaVersion is the only host document format this package reads.
	HostSchemaVersion = 1

	// hostSignDomain differs from the builds list's, so a signature of one is never valid for the other.
	hostSignDomain = "lucx-plugins-host-v1\n"
)

var hostVersionPattern = regexp.MustCompile(`^host-([1-9][0-9]{0,8})$`)

// hostArches are the architectures every host release carries, in the order they are listed.
var hostArches = []string{"amd64", "arm64"}

// HostBinary is the plugin host for one architecture: a bare executable, not an archive.
type HostBinary struct {
	Arch   string `json:"arch"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

// HostIndex is the signed host.json: which build of the plugin host is current. It sits beside
// builds.json but is a document of its own, because installed clients read builds.json strictly.
type HostIndex struct {
	Schema      int          `json:"schema"`
	Sequence    int64        `json:"sequence"`
	GeneratedAt time.Time    `json:"generatedAt"`
	Version     string       `json:"version"`
	Binaries    []HostBinary `json:"binaries"`
}

// HostNumber is the N of "host-N".
func HostNumber(version string) (int, bool) {
	m := hostVersionPattern.FindStringSubmatch(version)
	if m == nil {
		return 0, false
	}
	n, _ := strconv.Atoi(m[1])
	return n, true
}

// HostNewer reports whether candidate is a later host release than installed. An installed version
// that is not a release ("dev", empty) is older than any release.
func HostNewer(candidate, installed string) bool {
	c, ok := HostNumber(candidate)
	if !ok {
		return false
	}
	i, ok := HostNumber(installed)
	return !ok || c > i
}

func hostURL(version, arch string) string {
	return "https://github.com/" + OwnRepo + "/releases/download/" + version + "/lucx-plugin-host-linux-" + arch
}

// Validate checks everything a client must be able to rely on once the signature is good.
func (h *HostIndex) Validate() error {
	if h.Schema != HostSchemaVersion {
		return fmt.Errorf("unsupported host schema %d", h.Schema)
	}
	if h.Sequence < 1 {
		return errors.New("sequence must be at least 1")
	}
	if _, ok := HostNumber(h.Version); !ok {
		return fmt.Errorf("invalid host version %q: want host-<n>", h.Version)
	}
	if len(h.Binaries) != len(hostArches) {
		return fmt.Errorf("a host release lists exactly %v", hostArches)
	}
	seen := map[string]bool{}
	for n, b := range h.Binaries {
		if b.Arch != hostArches[n] {
			return fmt.Errorf("binaries must be %v in that order, found %q at position %d", hostArches, b.Arch, n)
		}
		if !hashPattern.MatchString(b.SHA256) {
			return fmt.Errorf("%s: %q is not a lowercase hex sha256", b.Arch, b.SHA256)
		}
		if seen[b.SHA256] {
			return fmt.Errorf("%s: the same hash is listed for two architectures", b.Arch)
		}
		seen[b.SHA256] = true
		if want := hostURL(h.Version, b.Arch); b.URL != want {
			return fmt.Errorf("%s: binary must be %s", b.Arch, want)
		}
	}
	return nil
}

// Binary finds the host for an architecture.
func (h *HostIndex) Binary(arch string) (HostBinary, bool) {
	for _, b := range h.Binaries {
		if b.Arch == arch {
			return b, true
		}
	}
	return HostBinary{}, false
}

// Set makes version the current host release (hashes by architecture) and raises the sequence.
// A release is immutable: the same version may be set again only with the same hashes, and an
// earlier version is refused.
func (h *HostIndex) Set(version string, sha256 map[string]string) error {
	next := HostIndex{Schema: HostSchemaVersion, Sequence: h.Sequence + 1, GeneratedAt: h.GeneratedAt, Version: version}
	for _, arch := range hostArches {
		next.Binaries = append(next.Binaries, HostBinary{Arch: arch, URL: hostURL(version, arch), SHA256: sha256[arch]})
	}
	if len(sha256) != len(hostArches) {
		return fmt.Errorf("need a hash for exactly %v", hostArches)
	}
	if err := next.Validate(); err != nil {
		return err
	}
	if h.Version != "" {
		cur, _ := HostNumber(h.Version)
		nxt, _ := HostNumber(version)
		switch {
		case nxt < cur:
			return fmt.Errorf("%s is older than the current %s", version, h.Version)
		case nxt == cur:
			for n, b := range next.Binaries {
				if b.SHA256 != h.Binaries[n].SHA256 {
					return fmt.Errorf("%s is already published with different binaries; a release is never changed", version)
				}
			}
		}
	}
	*h = next
	return nil
}

// MarshalHost writes the canonical form: 2-space JSON and a trailing newline.
func MarshalHost(h *HostIndex) ([]byte, error) {
	raw, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// ParseHost reads and validates a host document; unknown fields and trailing data are errors.
func ParseHost(data []byte) (*HostIndex, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var h HostIndex
	if err := dec.Decode(&h); err != nil {
		return nil, fmt.Errorf("invalid host document: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("invalid host document: trailing data")
	}
	if err := h.Validate(); err != nil {
		return nil, err
	}
	return &h, nil
}

// SignHost signs a host document.
func SignHost(key ed25519.PrivateKey, data []byte) string { return signWith(hostSignDomain, key, data) }

// VerifyHost accepts the host document if any key of the list signed it.
func VerifyHost(data []byte, sig string, keys []ed25519.PublicKey) error {
	return verifyWith(hostSignDomain, data, sig, keys)
}
