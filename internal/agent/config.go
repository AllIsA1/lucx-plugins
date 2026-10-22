// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

// Package agent keeps a server's panel binary in step with the signed builds list: it recognises
// an original upstream binary by its hash and replaces it with the matching build that has the
// plugin system, and it puts the original back on request. It never touches a binary it does
// not recognise.
package agent

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"time"
)

const (
	// DefaultIndexURL is where the signed list is published.
	DefaultIndexURL = "https://raw.githubusercontent.com/AllIsA1/lucx-plugins/builds/builds.json"

	defaultBudget     = 30 * time.Second
	defaultMaxIndex   = 1 << 20
	defaultMaxArchive = 256 << 20
	defaultMaxBinary  = 512 << 20
	sanityTimeout     = 10 * time.Second
)

// Config places the agent on a server; zero values select the defaults.
type Config struct {
	PanelBinary string // /usr/local/x-ui/x-ui
	StateDir    string // /var/lib/lucx-plugins
	Arch        string // amd64 or arm64
	IndexURL    string
	Keys        []ed25519.PublicKey
	Client      *http.Client
	// Budget bounds AtStart as a whole so a slow network can never hold up the panel's start.
	Budget     time.Duration
	MaxArchive int64
	MaxBinary  int64
	Restart    func(context.Context) error
	Log        func(format string, args ...any)
}

// Agent runs the operations; all of them serialise on a lock file in StateDir.
type Agent struct{ cfg Config }

// New fills in the defaults.
func New(cfg Config) *Agent {
	if cfg.IndexURL == "" {
		cfg.IndexURL = DefaultIndexURL
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 10 * time.Minute}
	}
	if cfg.Budget <= 0 {
		cfg.Budget = defaultBudget
	}
	if cfg.MaxArchive <= 0 {
		cfg.MaxArchive = defaultMaxArchive
	}
	if cfg.MaxBinary <= 0 {
		cfg.MaxBinary = defaultMaxBinary
	}
	if cfg.Log == nil {
		cfg.Log = func(string, ...any) {}
	}
	if cfg.Restart == nil {
		cfg.Restart = func(context.Context) error { return nil }
	}
	return &Agent{cfg: cfg}
}
