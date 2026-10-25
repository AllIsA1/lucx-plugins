// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import (
	"context"
	"fmt"
	"strings"
)

// catalogEntry finds a plugin in the verified catalog (a stale one still counts: it was verified).
func (m *Manager) catalogEntry(ctx context.Context, id string) (CatalogEntry, error) {
	view := m.Catalog(ctx, false)
	if view.State != CatalogOK && view.State != CatalogStale {
		return CatalogEntry{}, fmt.Errorf("%w: %s", ErrCatalogUnavailable, view.State)
	}
	for _, p := range view.Plugins {
		if p.ID == id {
			return p.CatalogEntry, nil
		}
	}
	return CatalogEntry{}, ErrNotFound
}

// catalogMismatch says how a staged download differs from the catalog entry; nil when it is the signed one.
func (m *Manager) catalogMismatch(p *Preview, e *CatalogEntry) error {
	m.mu.Lock()
	staged := m.staged[p.Token]
	m.mu.Unlock()
	switch {
	case staged == nil:
		return ErrUnknownToken
	case p.Manifest.ID != e.ID:
		return fmt.Errorf("%w: the plugin id is %q, the catalog says %q", ErrCatalogMismatch, p.Manifest.ID, e.ID)
	case e.Track:
		// The catalog vouches for the repository, not for a commit: the operator confirms what each version asks for.
		return nil
	case p.Commit != e.Commit:
		return fmt.Errorf("%w: the commit is %s, the catalog says %s", ErrCatalogMismatch, p.Commit, e.Commit)
	case staged.manifestSHA256 != e.ManifestSHA256:
		return fmt.Errorf("%w: lucx-plugin.json differs", ErrCatalogMismatch)
	case !SamePermissionSet(p.Manifest.Permissions, e.Permissions):
		return fmt.Errorf("%w: the permissions differ", ErrCatalogMismatch)
	case p.Manifest.Type != e.Type || p.Manifest.Scope != e.Scope:
		return fmt.Errorf("%w: the type or scope differs", ErrCatalogMismatch)
	}
	return nil
}

// PreviewCatalog downloads a catalog plugin at its signed commit and checks it against the index
// before anything can be installed: a mirror or a rewritten repository cannot slip in other code.
func (m *Manager) PreviewCatalog(ctx context.Context, id string) (*Preview, error) {
	entry, err := m.catalogEntry(ctx, id)
	if err != nil {
		return nil, err
	}
	src := entry.Source
	src.Ref = entry.Commit
	if entry.Track {
		src.Ref = "" // the repository's default branch, so the install does not stay on the commit that was reviewed
	}
	p, err := m.previewSource(ctx, src)
	if err != nil {
		return nil, err
	}
	if err := m.catalogMismatch(p, &entry); err != nil {
		m.discard(p.Token)
		return nil, err
	}
	p.Catalog = ListingListed
	return p, nil
}

// listedEntry is the catalog entry of an installed plugin when the catalog is reachable (or was, and is kept) and lists
// the same repository under that id; false otherwise.
func (m *Manager) listedEntry(ctx context.Context, id string) (CatalogEntry, bool) {
	have, ok := m.reg.Get(id)
	if !ok {
		return CatalogEntry{}, false
	}
	entry, err := m.catalogEntry(ctx, id)
	if err != nil {
		return CatalogEntry{}, false
	}
	if !strings.EqualFold(have.Source.Owner, entry.Source.Owner) || !strings.EqualFold(have.Source.Repo, entry.Source.Repo) {
		return CatalogEntry{}, false
	}
	return entry, true
}

// PreviewCatalogUpdate is PreviewUpdate pinned to the commit the catalog signed for an installed plugin.
func (m *Manager) PreviewCatalogUpdate(ctx context.Context, id string) (*UpdatePreview, error) {
	entry, err := m.catalogEntry(ctx, id)
	if err != nil {
		return nil, err
	}
	if entry.Track {
		return m.previewUpdate(ctx, id, "", &entry)
	}
	return m.previewUpdate(ctx, id, entry.Commit, &entry)
}
