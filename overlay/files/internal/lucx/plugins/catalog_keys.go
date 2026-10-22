// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"crypto/ed25519"
	"os"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"
)

// builtinCatalogKeys are the base64 ed25519 public keys the project signs its catalog with: the
// current one first, then a spare for rotation.
var builtinCatalogKeys = []string{
	"AZmDWYfs1F2RY4uZfDpWMIwaJd0nDYiXUR05dRzRR8s=",
	"SNh6iS8wcOOAqprSXEq2tO4izBAeG4Cth56Iubj4l1U=",
}

// DefaultCatalogKeys are the built-in keys plus any in XUI_PLUGINS_CATALOG_KEYS (comma separated),
// which is how a self-hosted catalog is trusted. An unreadable key is skipped, never fatal.
func DefaultCatalogKeys() []ed25519.PublicKey {
	var list []string
	list = append(list, builtinCatalogKeys...)
	for _, item := range strings.Split(os.Getenv("XUI_PLUGINS_CATALOG_KEYS"), ",") {
		if strings.TrimSpace(item) != "" {
			list = append(list, item)
		}
	}
	var keys []ed25519.PublicKey
	for _, item := range list {
		parsed, err := ParseCatalogKeys([]string{item})
		if err != nil {
			logger.Warning("plugins: ignoring a catalog key:", err)
			continue
		}
		keys = append(keys, parsed...)
	}
	return keys
}
