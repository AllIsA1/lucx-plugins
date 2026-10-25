// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const registrySchema = 1

var (
	ErrNotFound        = errors.New("plugin not found")
	ErrCorruptRegistry = errors.New("plugin registry is corrupt")
)

// Entry is the persisted record of one installed plugin.
type Entry struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	Version         string           `json:"version"`
	Type            string           `json:"type"`
	Scope           string           `json:"scope"`
	Source          Source           `json:"source"`
	Commit          string           `json:"commit"`
	ManifestSHA256  string           `json:"manifestSha256,omitempty"`
	Enabled         bool             `json:"enabled"`
	DisabledReason  string           `json:"disabledReason,omitempty"`
	ManagedByMaster bool             `json:"managedByMaster,omitempty"`
	InstallHookDone bool             `json:"installHookDone,omitempty"`
	UI              UISpec           `json:"ui"`
	Seams           []Seam           `json:"seams,omitempty"`
	Requires        []string         `json:"requires,omitempty"`
	Previous        *PreviousVersion `json:"previous,omitempty"`
	InstalledAt     time.Time        `json:"installedAt"`
	Permissions     []string         `json:"permissions"`
}

type registrySettings struct {
	ArchiveBase           string `json:"archiveBase,omitempty"`
	AllowMasterManagement bool   `json:"allowMasterManagement,omitempty"`
	CatalogBase           string `json:"catalogBase,omitempty"`
}

type registryFile struct {
	Schema   int              `json:"schema"`
	Settings registrySettings `json:"settings"`
	Plugins  []Entry          `json:"plugins"`
}

// Registry stores plugin records in a JSON file next to the panel database,
// outside it, so no database migration is involved.
type Registry struct {
	mu   sync.Mutex
	path string
	data registryFile
}

// OpenRegistry loads path; a missing file is an empty registry. A corrupt or
// newer-schema file is reported and never overwritten.
func OpenRegistry(path string) (*Registry, error) {
	r := &Registry{path: path, data: registryFile{Schema: registrySchema}}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	var loaded registryFile
	if err := json.Unmarshal(raw, &loaded); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCorruptRegistry, err)
	}
	if loaded.Schema > registrySchema {
		return nil, fmt.Errorf("%w: schema %d is newer than supported %d", ErrCorruptRegistry, loaded.Schema, registrySchema)
	}
	loaded.Schema = registrySchema
	r.data = loaded
	return r, nil
}

// List returns copies of all entries sorted by id.
func (r *Registry) List() []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Entry, len(r.data.Plugins))
	for i, e := range r.data.Plugins {
		out[i] = cloneEntry(e)
	}
	slices.SortFunc(out, func(a, b Entry) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// Get returns a copy of the entry with the given id.
func (r *Registry) Get(id string) (Entry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.data.Plugins {
		if e.ID == id {
			return cloneEntry(e), true
		}
	}
	return Entry{}, false
}

// Put inserts or replaces an entry and persists the registry.
func (r *Registry) Put(e Entry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := r.cloneDataLocked()
	e = cloneEntry(e)
	if i := slices.IndexFunc(next.Plugins, func(x Entry) bool { return x.ID == e.ID }); i >= 0 {
		next.Plugins[i] = e
	} else {
		next.Plugins = append(next.Plugins, e)
	}
	return r.commitLocked(next)
}

// Delete removes an entry and persists the registry.
func (r *Registry) Delete(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := r.cloneDataLocked()
	i := slices.IndexFunc(next.Plugins, func(x Entry) bool { return x.ID == id })
	if i < 0 {
		return ErrNotFound
	}
	next.Plugins = slices.Delete(next.Plugins, i, i+1)
	return r.commitLocked(next)
}

// ArchiveBase returns the operator-configured mirror base, "" when unset.
func (r *Registry) ArchiveBase() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.data.Settings.ArchiveBase
}

// SetArchiveBase validates and stores the mirror base; "" clears it.
func (r *Registry) SetArchiveBase(raw string) error {
	value := ""
	if strings.TrimSpace(raw) != "" {
		v, err := ValidateArchiveBase(raw)
		if err != nil {
			return err
		}
		value = v
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	next := r.cloneDataLocked()
	next.Settings.ArchiveBase = value
	return r.commitLocked(next)
}

// CatalogBase returns the operator-configured catalog mirror, "" when unset.
func (r *Registry) CatalogBase() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.data.Settings.CatalogBase
}

// SetCatalogBase validates and stores the catalog mirror; "" clears it.
func (r *Registry) SetCatalogBase(raw string) error {
	value := ""
	if strings.TrimSpace(raw) != "" {
		v, err := ValidateArchiveBase(raw)
		if err != nil {
			return err
		}
		value = v
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	next := r.cloneDataLocked()
	next.Settings.CatalogBase = value
	return r.commitLocked(next)
}

// AllowMasterManagement reports whether the node owner let a master panel
// install and control plugins here. Off until someone turns it on.
func (r *Registry) AllowMasterManagement() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.data.Settings.AllowMasterManagement
}

// SetAllowMasterManagement stores the node owner's decision.
func (r *Registry) SetAllowMasterManagement(allow bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := r.cloneDataLocked()
	next.Settings.AllowMasterManagement = allow
	return r.commitLocked(next)
}

func cloneSeams(in []Seam) []Seam {
	if in == nil {
		return nil
	}
	out := make([]Seam, len(in))
	for i, s := range in {
		s.Methods = slices.Clone(s.Methods)
		out[i] = s
	}
	return out
}

func cloneEntry(e Entry) Entry {
	e.Permissions = slices.Clone(e.Permissions)
	e.UI = UISpec{Menu: slices.Clone(e.UI.Menu), Replaces: slices.Clone(e.UI.Replaces)}
	e.Seams = cloneSeams(e.Seams)
	if e.Previous != nil {
		prev := *e.Previous
		prev.Seams = cloneSeams(prev.Seams)
		prev.Permissions = slices.Clone(prev.Permissions)
		prev.UI = UISpec{Menu: slices.Clone(prev.UI.Menu), Replaces: slices.Clone(prev.UI.Replaces)}
		e.Previous = &prev
	}
	return e
}

func (r *Registry) cloneDataLocked() registryFile {
	next := r.data
	next.Plugins = make([]Entry, len(r.data.Plugins))
	for i, e := range r.data.Plugins {
		next.Plugins[i] = cloneEntry(e)
	}
	return next
}

// commitLocked swaps in next only after it reached the disk.
func (r *Registry) commitLocked(next registryFile) error {
	if err := writeFileAtomic(r.path, &next); err != nil {
		return err
	}
	r.data = next
	return nil
}

func writeFileAtomic(path string, data *registryFile) error {
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".plugins-*.tmp")
	if err != nil {
		return err
	}
	cleanup := func() { _ = os.Remove(tmp.Name()) }
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		cleanup()
		return err
	}
	return nil
}
