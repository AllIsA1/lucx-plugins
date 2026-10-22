// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

const (
	// CatalogIndexFile and CatalogSigFile are the two files a catalog repository publishes.
	CatalogIndexFile = "index.json"
	CatalogSigFile   = "index.json.sig"

	MaxCatalogBytes   = 1 << 20
	MaxCatalogPlugins = 500
	catalogDomain     = "lucx-plugins-index-v1\n"
)

var (
	ErrCatalogSignature = errors.New("the catalog signature is not valid for any trusted key")
	ErrCatalogNoKeys    = errors.New("no catalog signing key is configured")

	verifiedDatePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	manifestHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// CatalogVerified records who reviewed a plugin and when.
type CatalogVerified struct {
	By    string `json:"by"`
	At    string `json:"at"`
	Notes string `json:"notes,omitempty"`
}

// CatalogEntry is one plugin the catalog vouches for, at one commit.
type CatalogEntry struct {
	ID             string           `json:"id"`
	Name           string           `json:"name"`
	Summary        string           `json:"summary"`
	Repo           string           `json:"repo"`
	Commit         string           `json:"commit"`
	Version        string           `json:"version"`
	ManifestSHA256 string           `json:"manifestSha256"`
	Type           string           `json:"type"`
	Scope          string           `json:"scope"`
	Permissions    []string         `json:"permissions"`
	Icon           string           `json:"icon,omitempty"`
	License        string           `json:"license,omitempty"`
	Author         string           `json:"author,omitempty"`
	Verified       *CatalogVerified `json:"verified,omitempty"`
	Source         Source           `json:"-"`
}

// CatalogIndex is the signed index.json.
type CatalogIndex struct {
	Schema      int            `json:"schema"`
	Sequence    int64          `json:"sequence"`
	GeneratedAt time.Time      `json:"generatedAt"`
	Plugins     []CatalogEntry `json:"plugins"`
}

// SignCatalog signs index.json; the result is the content of index.json.sig.
func SignCatalog(key ed25519.PrivateKey, index []byte) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(key, catalogMessage(index)))
}

func catalogMessage(index []byte) []byte {
	return append([]byte(catalogDomain), index...)
}

// VerifyCatalog accepts a signature made by any of keys; the list is how a key is rotated.
func VerifyCatalog(index []byte, sig string, keys []ed25519.PublicKey) error {
	if len(keys) == 0 {
		return ErrCatalogNoKeys
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(sig))
	if err != nil || len(raw) != ed25519.SignatureSize {
		return fmt.Errorf("%w: unreadable signature", ErrCatalogSignature)
	}
	msg := catalogMessage(index)
	for _, key := range keys {
		if ed25519.Verify(key, msg, raw) {
			return nil
		}
	}
	return ErrCatalogSignature
}

// ParseCatalogKeys decodes base64 ed25519 public keys.
func ParseCatalogKeys(list []string) ([]ed25519.PublicKey, error) {
	keys := make([]ed25519.PublicKey, 0, len(list))
	for _, item := range list {
		raw, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(item))
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("invalid catalog key %q: want a base64 ed25519 public key", item)
		}
		keys = append(keys, ed25519.PublicKey(raw))
	}
	return keys, nil
}

// ParseCatalogIndex reads and validates a catalog. The signature must be checked first; unknown fields
// are ignored so a newer index tool can add to the format.
func ParseCatalogIndex(raw []byte) (*CatalogIndex, error) {
	if len(raw) > MaxCatalogBytes {
		return nil, fmt.Errorf("the catalog is larger than %d bytes", MaxCatalogBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	var idx CatalogIndex
	if err := dec.Decode(&idx); err != nil {
		return nil, fmt.Errorf("the catalog is not valid JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("the catalog has data after the JSON document")
	}
	switch {
	case idx.Schema != 1:
		return nil, fmt.Errorf("catalog schema %d is not supported by this panel", idx.Schema)
	case idx.Sequence < 1:
		return nil, errors.New("the catalog has no sequence number")
	case idx.GeneratedAt.IsZero():
		return nil, errors.New("the catalog has no generatedAt time")
	case len(idx.Plugins) > MaxCatalogPlugins:
		return nil, fmt.Errorf("the catalog lists more than %d plugins", MaxCatalogPlugins)
	}
	seen := map[string]bool{}
	for i := range idx.Plugins {
		e := &idx.Plugins[i]
		if err := e.validate(); err != nil {
			return nil, fmt.Errorf("catalog entry %d (%q): %w", i+1, e.ID, err)
		}
		if seen[e.ID] {
			return nil, fmt.Errorf("catalog lists %q twice", e.ID)
		}
		seen[e.ID] = true
	}
	if idx.Plugins == nil {
		idx.Plugins = []CatalogEntry{}
	}
	return &idx, nil
}

func tooLong(s string, n int) bool { return len([]rune(s)) > n }

func (e *CatalogEntry) validate() error {
	src, err := ParseSource(e.Repo)
	switch {
	case !idPattern.MatchString(e.ID):
		return errors.New("invalid id")
	case e.Name == "" || tooLong(e.Name, 60):
		return errors.New("name must be 1-60 characters")
	case tooLong(e.Summary, 300):
		return errors.New("summary is longer than 300 characters")
	case err != nil:
		return fmt.Errorf("invalid repo: %w", err)
	case src.Ref != "":
		return errors.New("repo must be the repository link without a branch or tag; the commit pins the version")
	case !shaPattern.MatchString(e.Commit):
		return errors.New("commit must be a full lowercase SHA")
	case !versionPattern.MatchString(e.Version):
		return errors.New("invalid version")
	case !manifestHashPattern.MatchString(e.ManifestSHA256):
		return errors.New("manifestSha256 must be 64 lowercase hex characters")
	case e.Type != TypeUI && e.Type != TypeProcess && e.Type != TypeHook:
		return errors.New("invalid type")
	case e.Scope != ScopeMaster && e.Scope != ScopeNode && e.Scope != ScopeBoth:
		return errors.New("invalid scope")
	case e.Icon != "" && !iconPattern.MatchString(e.Icon):
		return errors.New("invalid icon")
	case tooLong(e.License, 40) || tooLong(e.Author, 60):
		return errors.New("license or author is too long")
	}
	for _, p := range e.Permissions {
		if err := ValidatePermission(p); err != nil {
			return err
		}
	}
	if v := e.Verified; v != nil {
		if v.By == "" || tooLong(v.By, 40) || tooLong(v.Notes, 200) || !verifiedDatePattern.MatchString(v.At) {
			return errors.New("verified needs a reviewer (up to 40 characters), a YYYY-MM-DD date and notes up to 200 characters")
		}
	}
	e.Source = src
	return nil
}
