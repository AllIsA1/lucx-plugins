// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

// Package keys holds the public keys that builds.json signatures are checked against.
package keys

import (
	"crypto/ed25519"
	_ "embed"
	"strings"

	"github.com/AllIsA1/lucx-plugins/internal/builds"
)

//go:embed builds.pub
var raw string

// Builds returns the trusted keys: the current one and a spare for rotation.
func Builds() []ed25519.PublicKey {
	var list []string
	for _, line := range strings.Split(raw, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			list = append(list, line)
		}
	}
	parsed, err := builds.ParseKeys(list)
	if err != nil {
		panic("keys/builds.pub is malformed: " + err.Error())
	}
	return parsed
}
