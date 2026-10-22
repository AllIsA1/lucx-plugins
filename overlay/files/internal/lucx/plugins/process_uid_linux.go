// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

//go:build linux

package plugins

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

const (
	uidBase  = 40000
	uidLimit = 59999
)

// uidMap gives every plugin its own stable system user id, kept on disk so a
// plugin keeps owning its data across restarts.
type uidMap struct {
	path  string
	limit int

	mu  sync.Mutex
	ids map[string]int
}

func newUIDMap(path string) *uidMap { return &uidMap{path: path, limit: uidLimit} }

func (m *uidMap) load() error {
	if m.ids != nil {
		return nil
	}
	raw, err := os.ReadFile(m.path)
	if errors.Is(err, fs.ErrNotExist) {
		m.ids = map[string]int{}
		return nil
	}
	if err != nil {
		return err
	}
	ids := map[string]int{}
	if err := json.Unmarshal(raw, &ids); err != nil {
		return fmt.Errorf("plugin user map %s is damaged (fix or remove it by hand): %w", m.path, err)
	}
	m.ids = ids
	return nil
}

// For returns the user id of plugin id, allocating the lowest free one on first use.
func (m *uidMap) For(id string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.load(); err != nil {
		return 0, err
	}
	if uid, ok := m.ids[id]; ok {
		return uid, nil
	}
	used := map[int]bool{}
	for _, uid := range m.ids {
		used[uid] = true
	}
	for uid := uidBase; uid <= m.limit; uid++ {
		if used[uid] {
			continue
		}
		m.ids[id] = uid
		if err := m.save(); err != nil {
			delete(m.ids, id)
			return 0, err
		}
		return uid, nil
	}
	return 0, fmt.Errorf("no free plugin user ids in %d-%d", uidBase, m.limit)
}

func (m *uidMap) save() error {
	raw, err := json.MarshalIndent(m.ids, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0o711); err != nil {
		return err
	}
	return writeSecretFile(m.path, raw, 0o600)
}

// checkSearchable fails when a directory on the way to path cannot be
// entered by other users, which a plugin running as its own user needs.
func checkSearchable(path string) error {
	for dir := path; ; dir = filepath.Dir(dir) {
		st, err := os.Stat(dir)
		if err != nil {
			return err
		}
		if st.Mode().Perm()&0o001 == 0 {
			return fmt.Errorf("directory %s is not searchable by other users (chmod o+x): plugins run as their own unprivileged user", dir)
		}
		if dir == filepath.Dir(dir) {
			return nil
		}
	}
}
