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
	// SidecarSchemaVersion is the only sidecar document format this package reads.
	SidecarSchemaVersion = 1

	// sidecarSignDomain differs from the other documents', so a signature of one is never valid for another.
	sidecarSignDomain = "lucx-plugins-sidecars-v1\n"

	maxSidecars = 16
)

var (
	// sidecarNames are the helper programs of the panel this list may carry; each is built by our CI from a
	// reviewed upstream commit and goes to <panel dir>/bin/<name>-linux-<arch>.
	sidecarNames = map[string]bool{"openflux": true}

	sidecarReleasePattern = regexp.MustCompile(`^sidecar-([a-z]+)-(v\d+\.\d+\.\d+)-([1-9][0-9]{0,5})$`)
	sidecarVersionPattern = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)
	commitPattern         = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// Sidecar is one helper program for one architecture: a bare executable, not an archive.
type Sidecar struct {
	Name    string `json:"name"`
	Version string `json:"version"` // the upstream tag it was built from
	Commit  string `json:"commit"`  // the upstream commit of that tag
	Release string `json:"release"` // our release that holds the binaries
	Arch    string `json:"arch"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
}

// SidecarIndex is the signed sidecars.json: which build of each helper program is current. It is a
// document of its own because installed clients read builds.json strictly.
type SidecarIndex struct {
	Schema      int       `json:"schema"`
	Sequence    int64     `json:"sequence"`
	GeneratedAt time.Time `json:"generatedAt"`
	Sidecars    []Sidecar `json:"sidecars"`
}

func sidecarURL(release, name, arch string) string {
	return "https://github.com/" + OwnRepo + "/releases/download/" + release + "/" + name + "-linux-" + arch
}

func sidecarVersionLess(a, b string) bool {
	pa, pb := sidecarVersionPattern.FindStringSubmatch(a), sidecarVersionPattern.FindStringSubmatch(b)
	if pa == nil || pb == nil {
		return false
	}
	for i := 1; i <= 3; i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x < y
		}
	}
	return false
}

func (s Sidecar) validate() error {
	m := sidecarReleasePattern.FindStringSubmatch(s.Release)
	switch {
	case !sidecarNames[s.Name]:
		return fmt.Errorf("unknown sidecar %q", s.Name)
	case s.Arch != "amd64" && s.Arch != "arm64":
		return fmt.Errorf("%s: unsupported arch %q", s.Name, s.Arch)
	case !sidecarVersionPattern.MatchString(s.Version):
		return fmt.Errorf("%s: invalid version %q", s.Name, s.Version)
	case !commitPattern.MatchString(s.Commit):
		return fmt.Errorf("%s: commit must be a full lowercase SHA", s.Name)
	case !hashPattern.MatchString(s.SHA256):
		return fmt.Errorf("%s/%s: %q is not a lowercase hex sha256", s.Name, s.Arch, s.SHA256)
	case m == nil || m[1] != s.Name || m[2] != s.Version:
		return fmt.Errorf("%s: release %q must be sidecar-%s-%s-<n>", s.Name, s.Release, s.Name, s.Version)
	case s.URL != sidecarURL(s.Release, s.Name, s.Arch):
		return fmt.Errorf("%s/%s: binary must be %s", s.Name, s.Arch, sidecarURL(s.Release, s.Name, s.Arch))
	}
	return nil
}

// Validate checks everything a client must be able to rely on once the signature is good.
func (i *SidecarIndex) Validate() error {
	if i.Schema != SidecarSchemaVersion {
		return fmt.Errorf("unsupported sidecar schema %d", i.Schema)
	}
	if i.Sequence < 1 {
		return errors.New("sequence must be at least 1")
	}
	if len(i.Sidecars) > maxSidecars {
		return fmt.Errorf("more than %d sidecars", maxSidecars)
	}
	type key struct{ name, arch string }
	seen := map[key]Sidecar{}
	hashes := map[string]bool{}
	for _, s := range i.Sidecars {
		if err := s.validate(); err != nil {
			return err
		}
		k := key{s.Name, s.Arch}
		if _, dup := seen[k]; dup {
			return fmt.Errorf("%s/%s is listed twice", s.Name, s.Arch)
		}
		if hashes[s.SHA256] {
			return fmt.Errorf("%s/%s: the same hash is listed twice", s.Name, s.Arch)
		}
		hashes[s.SHA256] = true
		seen[k] = s
	}
	for k, s := range seen {
		other := "amd64"
		if k.arch == "amd64" {
			other = "arm64"
		}
		o, ok := seen[key{k.name, other}]
		if !ok {
			return fmt.Errorf("%s must be listed for both architectures", k.name)
		}
		if o.Release != s.Release || o.Version != s.Version || o.Commit != s.Commit {
			return fmt.Errorf("%s: both architectures must come from one release", k.name)
		}
	}
	return nil
}

// Find returns the sidecar of a name for an architecture.
func (i *SidecarIndex) Find(name, arch string) (Sidecar, bool) {
	for _, s := range i.Sidecars {
		if s.Name == name && s.Arch == arch {
			return s, true
		}
	}
	return Sidecar{}, false
}

// Set makes release the current build of a sidecar (hashes by architecture) and raises the sequence.
// A release is immutable: the same release may be set again only with the same hashes, and an older
// upstream version than the listed one is refused.
func (i *SidecarIndex) Set(name, version, commit, release string, sha256 map[string]string) error {
	if len(sha256) != 2 {
		return errors.New("need a hash for exactly amd64 and arm64")
	}
	next := SidecarIndex{Schema: SidecarSchemaVersion, Sequence: i.Sequence + 1, GeneratedAt: i.GeneratedAt}
	for _, s := range i.Sidecars {
		if s.Name != name {
			next.Sidecars = append(next.Sidecars, s)
		}
	}
	for _, arch := range []string{"amd64", "arm64"} {
		next.Sidecars = append(next.Sidecars, Sidecar{
			Name: name, Version: version, Commit: commit, Release: release, Arch: arch,
			URL: sidecarURL(release, name, arch), SHA256: sha256[arch],
		})
	}
	if err := next.Validate(); err != nil {
		return err
	}
	for _, cur := range i.Sidecars {
		if cur.Name != name {
			continue
		}
		switch {
		case sidecarVersionLess(version, cur.Version):
			return fmt.Errorf("%s %s is older than the listed %s", name, version, cur.Version)
		case cur.Release == release && cur.SHA256 != sha256[cur.Arch]:
			return fmt.Errorf("%s is already published with different binaries; a release is never changed", release)
		}
	}
	*i = next
	return nil
}

// MarshalSidecars writes the canonical form: 2-space JSON and a trailing newline.
func MarshalSidecars(i *SidecarIndex) ([]byte, error) {
	raw, err := json.MarshalIndent(i, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// ParseSidecars reads and validates a sidecar document; unknown fields and trailing data are errors.
func ParseSidecars(data []byte) (*SidecarIndex, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var i SidecarIndex
	if err := dec.Decode(&i); err != nil {
		return nil, fmt.Errorf("invalid sidecar document: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("invalid sidecar document: trailing data")
	}
	if err := i.Validate(); err != nil {
		return nil, err
	}
	return &i, nil
}

// SignSidecars signs a sidecar document.
func SignSidecars(key ed25519.PrivateKey, data []byte) string {
	return signWith(sidecarSignDomain, key, data)
}

// VerifySidecars accepts the sidecar document if any key of the list signed it.
func VerifySidecars(data []byte, sig string, keys []ed25519.PublicKey) error {
	return verifyWith(sidecarSignDomain, data, sig, keys)
}
