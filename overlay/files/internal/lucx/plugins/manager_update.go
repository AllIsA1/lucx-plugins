// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/logger"
)

const previousSuffix = ".prev"

var (
	ErrUpdateIDMismatch     = errors.New("the repository now holds a different plugin")
	ErrUpdateSourceMismatch = errors.New("the download is not from this plugin's repository")
	ErrNoPreviousVersion    = errors.New("there is no earlier version to go back to")
	ErrUpdateRolledBack     = errors.New("the update did not work and the previous version was restored")
)

// PreviousVersion is what Rollback restores: the entry as it was before the last update.
type PreviousVersion struct {
	Name           string   `json:"name"`
	Version        string   `json:"version"`
	Type           string   `json:"type"`
	Scope          string   `json:"scope"`
	Commit         string   `json:"commit"`
	ManifestSHA256 string   `json:"manifestSha256,omitempty"`
	Permissions    []string `json:"permissions"`
	UI             UISpec   `json:"ui"`
}

// PermissionDiff compares the permissions an update asks for with those the operator granted.
type PermissionDiff struct {
	Added          []string `json:"added"`
	Removed        []string `json:"removed"`
	Kept           []string `json:"kept"`
	DangerousAdded []string `json:"dangerousAdded"`
}

// UpdateCurrent names the installed version an update is compared with.
type UpdateCurrent struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

// UpdatePreview describes a downloaded update awaiting confirmation. UpToDate means nothing was staged.
type UpdatePreview struct {
	Token             string         `json:"token"`
	Commit            string         `json:"commit"`
	Manifest          *Manifest      `json:"manifest"`
	Current           UpdateCurrent  `json:"current"`
	UpToDate          bool           `json:"upToDate"`
	Diff              PermissionDiff `json:"diff"`
	TypeChanged       bool           `json:"typeChanged"`
	ScopeChanged      bool           `json:"scopeChanged"`
	NeedsConfirmation bool           `json:"needsConfirmation"`
	ExpiresAt         time.Time      `json:"expiresAt"`
}

func diffPermissions(old, next []string) PermissionDiff {
	d := PermissionDiff{Added: []string{}, Removed: []string{}, Kept: []string{}, DangerousAdded: []string{}}
	oldSet, nextSet := normalizePermissions(old), normalizePermissions(next)
	for _, p := range nextSet {
		switch {
		case slices.Contains(oldSet, p):
			d.Kept = append(d.Kept, p)
		default:
			d.Added = append(d.Added, p)
			if IsDangerousPermission(p) {
				d.DangerousAdded = append(d.DangerousAdded, p)
			}
		}
	}
	for _, p := range oldSet {
		if !slices.Contains(nextSet, p) {
			d.Removed = append(d.Removed, p)
		}
	}
	return d
}

// validUpdateRef accepts a branch, tag or SHA that is one path segment and cannot be taken for an option.
func validUpdateRef(ref string) bool {
	return len(ref) <= maxRefLen && refPartRegexp.MatchString(ref) && !strings.HasPrefix(ref, "-") && !strings.Contains(ref, "..")
}

// PreviewUpdate downloads the plugin's own repository again (at ref, or where it was installed from)
// and says what an update would change. Nothing on disk changes until ApplyUpdate.
func (m *Manager) PreviewUpdate(ctx context.Context, id, ref string) (*UpdatePreview, error) {
	return m.previewUpdate(ctx, id, ref, nil)
}

// previewUpdate is PreviewUpdate; with expect set the download must also be exactly what the catalog signed.
func (m *Manager) previewUpdate(ctx context.Context, id, ref string, expect *CatalogEntry) (*UpdatePreview, error) {
	entry, ok := m.reg.Get(id)
	if !ok {
		return nil, ErrNotFound
	}
	src := entry.Source
	if ref != "" {
		if !validUpdateRef(ref) {
			return nil, fmt.Errorf("invalid ref %q: use a branch, tag or commit name", ref)
		}
		src.Ref = ref
	}
	p, err := m.previewSource(ctx, src)
	if err != nil {
		return nil, err
	}
	switch {
	case p.Manifest.ID != id:
		m.discard(p.Token)
		return nil, fmt.Errorf("%w: expected %q, found %q", ErrUpdateIDMismatch, id, p.Manifest.ID)
	case expect != nil && m.catalogMismatch(p, expect) != nil:
		err := m.catalogMismatch(p, expect)
		m.discard(p.Token)
		return nil, err
	case p.Commit == entry.Commit:
		m.discard(p.Token)
		return &UpdatePreview{Commit: p.Commit, Manifest: p.Manifest, Current: UpdateCurrent{entry.Version, entry.Commit}, UpToDate: true}, nil
	}
	up := &UpdatePreview{
		Token:        p.Token,
		Commit:       p.Commit,
		Manifest:     p.Manifest,
		Current:      UpdateCurrent{entry.Version, entry.Commit},
		Diff:         diffPermissions(entry.Permissions, p.Manifest.Permissions),
		TypeChanged:  p.Manifest.Type != entry.Type,
		ScopeChanged: p.Manifest.Scope != entry.Scope,
		ExpiresAt:    p.ExpiresAt,
	}
	up.NeedsConfirmation = len(up.Diff.Added) > 0 || up.TypeChanged || up.ScopeChanged
	return up, nil
}

func snapshot(e Entry) *PreviousVersion {
	return &PreviousVersion{
		Name: e.Name, Version: e.Version, Type: e.Type, Scope: e.Scope, Commit: e.Commit,
		ManifestSHA256: e.ManifestSHA256, Permissions: slices.Clone(e.Permissions), UI: e.UI,
	}
}

func restore(e Entry, p *PreviousVersion) Entry {
	e.Name, e.Version, e.Type, e.Scope, e.Commit = p.Name, p.Version, p.Type, p.Scope, p.Commit
	e.ManifestSHA256, e.Permissions, e.UI, e.Previous, e.DisabledReason = p.ManifestSHA256, slices.Clone(p.Permissions), p.UI, nil, ""
	return e
}

// ApplyUpdate replaces the plugin's code with a previewed update once the operator accepted exactly its
// permissions. The old code is kept for Rollback; data, enabled state and install time stay as they were.
func (m *Manager) ApplyUpdate(ctx context.Context, id, token string, accepted []string) (Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.reg.Get(id)
	if !ok {
		return Entry{}, ErrNotFound
	}
	s, ok := m.staged[token]
	if !ok {
		return Entry{}, ErrUnknownToken
	}
	manifest := s.preview.Manifest
	switch {
	case manifest.ID != id:
		return Entry{}, ErrUpdateIDMismatch
	case !strings.EqualFold(s.preview.Source.Owner, entry.Source.Owner) || !strings.EqualFold(s.preview.Source.Repo, entry.Source.Repo):
		return Entry{}, ErrUpdateSourceMismatch
	case !SamePermissionSet(accepted, manifest.Permissions):
		return Entry{}, ErrPermissionsMismatch
	}
	if m.proc != nil {
		if err := m.proc.StopPlugin(ctx, id); err != nil {
			return Entry{}, fmt.Errorf("cannot stop the plugin process, nothing was changed: %w", err)
		}
	}
	target := filepath.Join(m.cfg.CodeDir, id)
	prev := target + previousSuffix
	if err := os.RemoveAll(prev); err != nil {
		return Entry{}, err
	}
	if err := os.Rename(target, prev); err != nil {
		return Entry{}, err
	}
	if err := os.Rename(s.dir, target); err != nil {
		_ = os.Rename(prev, target)
		return Entry{}, err
	}
	next := entry
	next.Name, next.Version, next.Type, next.Scope = manifest.Name, manifest.Version, manifest.Type, manifest.Scope
	next.Commit, next.ManifestSHA256, next.UI, next.DisabledReason = s.preview.Commit, s.manifestSHA256, manifest.UI, ""
	next.Permissions = normalizePermissions(manifest.Permissions)
	next.Previous = snapshot(entry)
	if err := m.reg.Put(next); err != nil {
		_ = os.Rename(target, s.dir)
		_ = os.Rename(prev, target)
		return Entry{}, err
	}
	delete(m.staged, token)
	logger.Infof("plugins: updated %s %s -> %s (%s)", id, entry.Version, next.Version, next.Commit)
	return next, nil
}

// Rollback puts the version from before the last update back. The code it replaces is dropped.
func (m *Manager) Rollback(ctx context.Context, id string) (Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.reg.Get(id)
	if !ok {
		return Entry{}, ErrNotFound
	}
	if entry.Previous == nil {
		return Entry{}, ErrNoPreviousVersion
	}
	target := filepath.Join(m.cfg.CodeDir, id)
	prev := target + previousSuffix
	if st, err := os.Stat(prev); err != nil || !st.IsDir() {
		return Entry{}, fmt.Errorf("%w: the saved code is gone", ErrNoPreviousVersion)
	}
	if m.proc != nil {
		if err := m.proc.StopPlugin(ctx, id); err != nil {
			return Entry{}, fmt.Errorf("cannot stop the plugin process, nothing was changed: %w", err)
		}
	}
	aside := target + ".rollback"
	if err := os.RemoveAll(aside); err != nil {
		return Entry{}, err
	}
	if err := os.Rename(target, aside); err != nil {
		return Entry{}, err
	}
	if err := os.Rename(prev, target); err != nil {
		_ = os.Rename(aside, target)
		return Entry{}, err
	}
	next := restore(entry, entry.Previous)
	if err := m.reg.Put(next); err != nil {
		_ = os.Rename(target, prev)
		_ = os.Rename(aside, target)
		return Entry{}, err
	}
	_ = os.RemoveAll(aside)
	logger.Infof("plugins: rolled %s back to %s (%s)", id, next.Version, next.Commit)
	return next, nil
}
