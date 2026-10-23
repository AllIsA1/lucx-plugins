// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

// Package builds is the signed list that tells a server which build of the plugin system matches
// the panel binary it runs: the hash of each original upstream `x-ui` maps to our build of the
// same release. CI writes it, the installer on the server verifies and reads it.
package builds

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// SchemaVersion is the only document format this package reads.
	SchemaVersion = 1
	// UpstreamRepo is where the panel's own releases are published.
	UpstreamRepo = "AlexeyLCP/lucx-ui"
	// OwnRepo is where our builds are published.
	OwnRepo = "AllIsA1/lucx-plugins"

	signDomain = "lucx-plugins-builds-v1\n"
)

var (
	tagPattern     = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)-lucx\.(\d+)$`)
	releasePattern = regexp.MustCompile(`^(v\d+\.\d+\.\d+-lucx\.\d+)-plugins\.(\d+)$`)
	hashPattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Archive is a downloadable tarball and the hash it must have.
type Archive struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

// Build is one upstream release for one architecture.
type Build struct {
	Upstream             string  `json:"upstream"`
	Arch                 string  `json:"arch"`
	OriginalBinarySha256 string  `json:"originalBinarySha256"`
	OriginalArchive      Archive `json:"originalArchive"`
	Release              string  `json:"release"`
	Build                Archive `json:"build"`
	BinarySha256         string  `json:"binarySha256"`
}

// Index is the whole document.
type Index struct {
	Schema      int       `json:"schema"`
	Sequence    int64     `json:"sequence"`
	GeneratedAt time.Time `json:"generatedAt"`
	Builds      []Build   `json:"builds"`
}

// Kind says what a binary on disk is, judged by its hash.
type Kind int

const (
	Unknown  Kind = iota // not in the list: never touched
	Original             // an upstream binary we have a build for
	Ours                 // already our build
)

func (b Build) originalURL() string {
	return "https://github.com/" + UpstreamRepo + "/releases/download/" + b.Upstream + "/x-ui-linux-" + b.Arch + ".tar.gz"
}

func (b Build) buildURL() string {
	return "https://github.com/" + OwnRepo + "/releases/download/" + b.Release + "/x-ui-plugins-linux-" + b.Arch + ".tar.gz"
}

func (b Build) validate() error {
	if !tagPattern.MatchString(b.Upstream) {
		return fmt.Errorf("invalid upstream tag %q", b.Upstream)
	}
	if b.Arch != "amd64" && b.Arch != "arm64" {
		return fmt.Errorf("%s: unsupported arch %q", b.Upstream, b.Arch)
	}
	for name, h := range map[string]string{
		"originalBinarySha256": b.OriginalBinarySha256, "binarySha256": b.BinarySha256,
		"original archive sha256": b.OriginalArchive.SHA256, "build archive sha256": b.Build.SHA256,
	} {
		if !hashPattern.MatchString(h) {
			return fmt.Errorf("%s/%s: %s is not a lowercase hex sha256", b.Upstream, b.Arch, name)
		}
	}
	if b.OriginalBinarySha256 == b.BinarySha256 {
		return fmt.Errorf("%s/%s: the original and our binary must differ", b.Upstream, b.Arch)
	}
	if m := releasePattern.FindStringSubmatch(b.Release); m == nil || m[1] != b.Upstream {
		return fmt.Errorf("%s/%s: release %q must be <upstream>-plugins.<n>", b.Upstream, b.Arch, b.Release)
	}
	if b.OriginalArchive.URL != b.originalURL() {
		return fmt.Errorf("%s/%s: original archive must be %s", b.Upstream, b.Arch, b.originalURL())
	}
	if b.Build.URL != b.buildURL() {
		return fmt.Errorf("%s/%s: build archive must be %s", b.Upstream, b.Arch, b.buildURL())
	}
	return nil
}

// version orders upstream tags numerically (lucx.99 before lucx.100).
func version(tag string) [4]int {
	var v [4]int
	if m := tagPattern.FindStringSubmatch(tag); m != nil {
		for i := range v {
			v[i], _ = strconv.Atoi(m[i+1])
		}
	}
	return v
}

func less(a, b Build) bool {
	va, vb := version(a.Upstream), version(b.Upstream)
	if va != vb {
		for i := range va {
			if va[i] != vb[i] {
				return va[i] < vb[i]
			}
		}
	}
	return a.Arch < b.Arch
}

// Validate checks everything a client must be able to rely on once the signature is good.
func (i *Index) Validate() error {
	if i.Schema != SchemaVersion {
		return fmt.Errorf("unsupported schema %d", i.Schema)
	}
	if i.Sequence < 1 {
		return errors.New("sequence must be at least 1")
	}
	seen, hashes := map[string]bool{}, map[string]bool{}
	for n, b := range i.Builds {
		if err := b.validate(); err != nil {
			return err
		}
		key := b.Upstream + "/" + b.Arch
		if seen[key] {
			return fmt.Errorf("duplicate build %s", key)
		}
		seen[key] = true
		if n > 0 && !less(i.Builds[n-1], b) {
			return fmt.Errorf("builds must be sorted by version and arch: %s comes after %s/%s", key, i.Builds[n-1].Upstream, i.Builds[n-1].Arch)
		}
		for _, h := range []string{b.OriginalBinarySha256, b.BinarySha256} {
			if hashes[h] {
				return fmt.Errorf("%s: the same original or build hash is listed twice (%s)", key, h)
			}
			hashes[h] = true
		}
	}
	return nil
}

// Add inserts a build or replaces the one for the same release and arch, and raises the sequence.
func (i *Index) Add(b Build) error {
	if err := b.validate(); err != nil {
		return err
	}
	kept := i.Builds[:0:0]
	for _, old := range i.Builds {
		if old.Upstream != b.Upstream || old.Arch != b.Arch {
			kept = append(kept, old)
		}
	}
	i.Builds = append(kept, b)
	sort.Slice(i.Builds, func(x, y int) bool { return less(i.Builds[x], i.Builds[y]) })
	i.Sequence++
	return nil
}

// Lookup finds the build a binary hash belongs to, as an upstream original or as our own build.
func (i *Index) Lookup(binarySha256, arch string) (*Build, Kind) {
	for n := range i.Builds {
		b := &i.Builds[n]
		if b.Arch != arch {
			continue
		}
		switch binarySha256 {
		case b.OriginalBinarySha256:
			return b, Original
		case b.BinarySha256:
			return b, Ours
		}
	}
	return nil, Unknown
}

// Marshal writes the canonical form: 2-space JSON and a trailing newline.
func Marshal(i *Index) ([]byte, error) {
	raw, err := json.MarshalIndent(i, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// Parse reads and validates a document; unknown fields and trailing data are errors.
func Parse(data []byte) (*Index, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var i Index
	if err := dec.Decode(&i); err != nil {
		return nil, fmt.Errorf("invalid builds document: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("invalid builds document: trailing data")
	}
	if err := i.Validate(); err != nil {
		return nil, err
	}
	return &i, nil
}

func message(domain string, data []byte) []byte { return append([]byte(domain), data...) }

// Sign returns the base64 signature of a document; the fixed prefix keeps it from being valid for anything else.
func Sign(key ed25519.PrivateKey, data []byte) string { return signWith(signDomain, key, data) }

func signWith(domain string, key ed25519.PrivateKey, data []byte) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(key, message(domain, data)))
}

// ErrSignature is returned for any signature that does not verify.
var ErrSignature = errors.New("the signature does not match the document")

// Verify accepts the document if any key of the list signed it.
func Verify(data []byte, sig string, keys []ed25519.PublicKey) error {
	return verifyWith(signDomain, data, sig, keys)
}

func verifyWith(domain string, data []byte, sig string, keys []ed25519.PublicKey) error {
	if len(keys) == 0 {
		return errors.New("no public keys to verify with")
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(sig))
	if err != nil || len(raw) != ed25519.SignatureSize {
		return fmt.Errorf("%w: unreadable signature", ErrSignature)
	}
	msg := message(domain, data)
	for _, key := range keys {
		if ed25519.Verify(key, msg, raw) {
			return nil
		}
	}
	return ErrSignature
}

// ParseKeys decodes base64 ed25519 public keys.
func ParseKeys(list []string) ([]ed25519.PublicKey, error) {
	keys := make([]ed25519.PublicKey, 0, len(list))
	for _, item := range list {
		raw, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(item))
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("invalid public key %q: want a base64 ed25519 key", item)
		}
		keys = append(keys, ed25519.PublicKey(raw))
	}
	return keys, nil
}
