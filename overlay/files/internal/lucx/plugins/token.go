// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// TokenPrefix routes a bearer token to plugin verification instead of the database.
	TokenPrefix = "lxp."
	// TokenScope is the api_token_scope the panel sets for an authenticated plugin.
	TokenScope = "plugin"

	keyLen        = 32
	tokenContext  = "lucx-plugin-token-v1"
	tokenFileName = "panel-token"
)

// TokenSigner issues and verifies stateless plugin API tokens: an HMAC over
// the plugin id, its build fingerprint and its permissions. Nothing is stored,
// so tokens survive a panel restart and die with any change to build or rights.
type TokenSigner struct{ key []byte }

// NewTokenSigner wraps a secret key from LoadOrCreateKey.
func NewTokenSigner(key []byte) *TokenSigner { return &TokenSigner{key: key} }

// LoadOrCreateKey reads the signing key, creating a random one (0600) on first use.
func LoadOrCreateKey(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		if len(raw) != keyLen {
			return nil, fmt.Errorf("plugin key %s has %d bytes, want %d; delete it to issue new tokens", path, len(raw), keyLen)
		}
		return raw, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key := make([]byte, keyLen)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

func (s *TokenSigner) mac(e Entry) []byte {
	h := hmac.New(sha256.New, s.key)
	h.Write([]byte(tokenContext))
	for _, part := range []string{e.ID, Fingerprint(e), strings.Join(normalizePermissions(e.Permissions), ",")} {
		h.Write([]byte{0})
		h.Write([]byte(part))
	}
	return h.Sum(nil)
}

// Issue returns the bearer token for an installed plugin.
func (s *TokenSigner) Issue(e Entry) string {
	return TokenPrefix + e.ID + "." + base64.RawURLEncoding.EncodeToString(s.mac(e))
}

// Verify checks a presented token against the current registry entry. The
// plugin must be installed and enabled, and its build and rights unchanged.
func (s *TokenSigner) Verify(token string, lookup func(id string) (Entry, bool)) (Entry, bool) {
	rest, ok := strings.CutPrefix(token, TokenPrefix)
	if !ok {
		return Entry{}, false
	}
	id, sig, ok := strings.Cut(rest, ".")
	if !ok || !idPattern.MatchString(id) {
		return Entry{}, false
	}
	e, found := lookup(id)
	if !found || !e.Enabled {
		return Entry{}, false
	}
	presented, err := base64.RawURLEncoding.Strict().DecodeString(sig)
	if err != nil || !hmac.Equal(presented, s.mac(e)) {
		return Entry{}, false
	}
	return e, true
}
