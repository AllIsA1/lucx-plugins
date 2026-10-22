// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"
)

// Catalog states a view can be in.
const (
	CatalogOK           = "ok"
	CatalogStale        = "stale"
	CatalogError        = "error"
	CatalogNotPublished = "not-published"
	CatalogUnconfigured = "unconfigured"
)

// How a catalog plugin relates to what is installed here.
const (
	CatalogAvailable       = "available"
	CatalogInstalled       = "installed"
	CatalogUpdateAvailable = "update-available"
	CatalogIDConflict      = "id-conflict"
)

const (
	// DefaultCatalogBase is where the project publishes its signed catalog.
	DefaultCatalogBase   = "https://raw.githubusercontent.com/AllIsA1/lucx-plugins-index/main"
	defaultCatalogMaxAge = 6 * time.Hour
	catalogFetchTimeout  = 10 * time.Second
	maxCatalogSigBytes   = 4096
)

var (
	ErrCatalogMismatch    = errors.New("the download does not match what the catalog signed")
	ErrCatalogUnavailable = errors.New("the catalog is not available")
	// ErrCatalogNotPublished is a 404 for index.json: nobody has published a catalog at that address yet.
	ErrCatalogNotPublished = errors.New("no catalog is published at this address yet")
	errFileNotFound        = errors.New("not found")
)

// CatalogPlugin is a catalog entry plus its relation to the installed plugins.
type CatalogPlugin struct {
	CatalogEntry
	Status    string         `json:"status"`
	Installed *UpdateCurrent `json:"installed,omitempty"`
}

// CatalogView is what the UI shows. A stale view still carries the last good catalog.
type CatalogView struct {
	State       string          `json:"state"`
	Error       string          `json:"error,omitempty"`
	Base        string          `json:"base"`
	CustomBase  string          `json:"customBase"`
	FetchedAt   time.Time       `json:"fetchedAt"`
	GeneratedAt time.Time       `json:"generatedAt"`
	Sequence    int64           `json:"sequence"`
	Plugins     []CatalogPlugin `json:"plugins"`
}

type catalogCacheFile struct {
	MaxSequence int64     `json:"maxSequence"`
	FetchedAt   time.Time `json:"fetchedAt"`
	Index       string    `json:"index"`
	Sig         string    `json:"sig"`
}

// catalogCache holds the last verified catalog; the highest sequence ever seen survives restarts.
type catalogCache struct {
	mu        sync.Mutex
	loaded    bool
	idx       *CatalogIndex
	fetchedAt time.Time
	maxSeq    int64
}

// CatalogBase is the effective catalog location: the operator's mirror, the configured base, the default.
func (m *Manager) CatalogBase() string {
	if v := m.reg.CatalogBase(); v != "" {
		return v
	}
	return m.cfg.CatalogBase
}

// CustomCatalogBase returns the operator-set mirror, "" when none is set.
func (m *Manager) CustomCatalogBase() string { return m.reg.CatalogBase() }

// SetCatalogBase stores a catalog mirror; "" restores the default.
func (m *Manager) SetCatalogBase(raw string) error { return m.reg.SetCatalogBase(raw) }

// Catalog returns the catalog, fetching it when force is set or the cache is older than the max age.
func (m *Manager) Catalog(ctx context.Context, force bool) CatalogView {
	c := &m.cat
	c.mu.Lock()
	defer c.mu.Unlock()
	view := CatalogView{Base: m.CatalogBase(), CustomBase: m.CustomCatalogBase(), Plugins: []CatalogPlugin{}}
	if len(m.cfg.CatalogKeys) == 0 {
		view.State = CatalogUnconfigured
		return view
	}
	m.loadCatalogCacheLocked()
	var fetchErr error
	if force || c.idx == nil || m.cfg.Now().Sub(c.fetchedAt) >= m.cfg.CatalogMaxAge {
		fetchErr = m.fetchCatalogLocked(ctx)
	}
	return m.catalogViewLocked(view, fetchErr)
}

func (m *Manager) catalogViewLocked(view CatalogView, fetchErr error) CatalogView {
	c := &m.cat
	switch {
	case fetchErr == nil:
		view.State = CatalogOK
	case c.idx != nil:
		view.State, view.Error = CatalogStale, fetchErr.Error()
	case errors.Is(fetchErr, ErrCatalogNotPublished):
		view.State = CatalogNotPublished
	default:
		view.State, view.Error = CatalogError, fetchErr.Error()
	}
	if c.idx == nil {
		return view
	}
	view.FetchedAt, view.GeneratedAt, view.Sequence = c.fetchedAt, c.idx.GeneratedAt, c.idx.Sequence
	for _, e := range c.idx.Plugins {
		view.Plugins = append(view.Plugins, m.catalogPlugin(e))
	}
	return view
}

func (m *Manager) catalogPlugin(e CatalogEntry) CatalogPlugin {
	p := CatalogPlugin{CatalogEntry: e, Status: CatalogAvailable}
	have, ok := m.reg.Get(e.ID)
	if !ok {
		return p
	}
	p.Installed = &UpdateCurrent{Version: have.Version, Commit: have.Commit}
	switch {
	case !strings.EqualFold(have.Source.Owner, e.Source.Owner) || !strings.EqualFold(have.Source.Repo, e.Source.Repo):
		p.Status = CatalogIDConflict
	case have.Commit == e.Commit:
		p.Status = CatalogInstalled
	default:
		p.Status = CatalogUpdateAvailable
	}
	return p
}

func (m *Manager) loadCatalogCacheLocked() {
	c := &m.cat
	if c.loaded {
		return
	}
	c.loaded = true
	raw, err := os.ReadFile(m.cfg.CatalogCachePath)
	if err != nil {
		return
	}
	var file catalogCacheFile
	if json.Unmarshal(raw, &file) != nil {
		return
	}
	c.maxSeq = file.MaxSequence
	// The cache is trusted no further than a download: it is verified again with today's keys.
	index := []byte(file.Index)
	if VerifyCatalog(index, file.Sig, m.cfg.CatalogKeys) != nil {
		return
	}
	if idx, err := ParseCatalogIndex(index); err == nil {
		c.idx, c.fetchedAt = idx, file.FetchedAt
	}
}

func (m *Manager) fetchCatalogLocked(ctx context.Context) error {
	c := &m.cat
	ctx, cancel := context.WithTimeout(ctx, catalogFetchTimeout)
	defer cancel()
	base := m.CatalogBase()
	index, err := m.fetchCapped(ctx, base+"/"+CatalogIndexFile, MaxCatalogBytes)
	if errors.Is(err, errFileNotFound) {
		return ErrCatalogNotPublished
	}
	if err != nil {
		return err
	}
	sig, err := m.fetchCapped(ctx, base+"/"+CatalogSigFile, maxCatalogSigBytes)
	if errors.Is(err, errFileNotFound) {
		return errors.New("the catalog has no signature file (index.json.sig)")
	}
	if err != nil {
		return err
	}
	if err := VerifyCatalog(index, string(sig), m.cfg.CatalogKeys); err != nil {
		return err
	}
	idx, err := ParseCatalogIndex(index)
	if err != nil {
		return err
	}
	if idx.Sequence < c.maxSeq {
		return fmt.Errorf("the catalog on offer (sequence %d) is older than the one already seen (%d)", idx.Sequence, c.maxSeq)
	}
	c.idx, c.fetchedAt, c.maxSeq = idx, m.cfg.Now(), idx.Sequence
	if err := writeJSONFileAtomic(m.cfg.CatalogCachePath, catalogCacheFile{
		MaxSequence: c.maxSeq, FetchedAt: c.fetchedAt, Index: string(index), Sig: strings.TrimSpace(string(sig)),
	}); err != nil {
		logger.Warning("plugins: cannot save the catalog cache:", err)
	}
	return nil
}

func (m *Manager) fetchCapped(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "lucx-ui-plugins")
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("catalog download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errFileNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("catalog download failed: HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("catalog download failed: %w", err)
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("the catalog file is larger than %d bytes", limit)
	}
	return raw, nil
}

// writeJSONFileAtomic stores v at path (0600) so a crash never leaves a half-written file.
func writeJSONFileAtomic(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".catalog-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Catalog listing of a downloaded plugin, as the UI words it.
const (
	ListingListed      = "listed"
	ListingUnlisted    = "unlisted"
	ListingUnavailable = "unavailable"
)

// catalogListing says whether the cached, verified catalog vouches for exactly this repository and commit.
// It never touches the network, so a link preview stays as fast as before.
func (m *Manager) catalogListing(p *Preview) string {
	c := &m.cat
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(m.cfg.CatalogKeys) == 0 {
		return ListingUnavailable
	}
	m.loadCatalogCacheLocked()
	if c.idx == nil {
		return ListingUnavailable
	}
	for _, e := range c.idx.Plugins {
		if e.Commit == p.Commit && strings.EqualFold(e.Source.Owner, p.Source.Owner) && strings.EqualFold(e.Source.Repo, p.Source.Repo) {
			return ListingListed
		}
	}
	return ListingUnlisted
}
