// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func sampleEntry(id string) Entry {
	return Entry{
		ID:          id,
		Name:        "Sample " + id,
		Version:     "1.0.0",
		Type:        TypeProcess,
		Scope:       ScopeMaster,
		Source:      Source{Owner: "acme", Repo: id},
		Commit:      testCommit,
		Permissions: []string{"network"},
		InstalledAt: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC),
	}
}

func TestRegistryMissingFileIsEmpty(t *testing.T) {
	r, err := OpenRegistry(filepath.Join(t.TempDir(), "plugins.json"))
	if err != nil {
		t.Fatalf("OpenRegistry: %v", err)
	}
	if got := r.List(); len(got) != 0 {
		t.Errorf("List = %+v, want empty", got)
	}
}

func TestRegistryPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugins.json")
	r, err := OpenRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Put(sampleEntry("zeta")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := r.Put(sampleEntry("alpha")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := r.SetArchiveBase("https://mirror.example/gh"); err != nil {
		t.Fatalf("SetArchiveBase: %v", err)
	}

	again, err := OpenRegistry(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	list := again.List()
	if len(list) != 2 || list[0].ID != "alpha" || list[1].ID != "zeta" {
		t.Fatalf("List = %+v, want [alpha zeta]", list)
	}
	if list[1].Commit != testCommit || list[1].Source.Owner != "acme" {
		t.Errorf("entry lost fields: %+v", list[1])
	}
	if got := again.ArchiveBase(); got != "https://mirror.example/gh" {
		t.Errorf("ArchiveBase = %q", got)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("registry mode = %v, want 0600", st.Mode().Perm())
	}
}

func TestRegistryPutReplacesAndDeleteRemoves(t *testing.T) {
	r, _ := OpenRegistry(filepath.Join(t.TempDir(), "plugins.json"))
	e := sampleEntry("alpha")
	_ = r.Put(e)
	e.Enabled = true
	e.Version = "2.0.0"
	_ = r.Put(e)
	got, ok := r.Get("alpha")
	if !ok || !got.Enabled || got.Version != "2.0.0" || len(r.List()) != 1 {
		t.Fatalf("Get = %+v ok=%v list=%d", got, ok, len(r.List()))
	}
	if err := r.Delete("alpha"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := r.Get("alpha"); ok {
		t.Error("entry still present after Delete")
	}
	if err := r.Delete("alpha"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete of missing entry = %v, want ErrNotFound", err)
	}
}

func TestRegistryCorruptFileIsReportedAndLeftUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugins.json")
	const junk = "{ this is not json"
	if err := os.WriteFile(path, []byte(junk), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := OpenRegistry(path)
	if !errors.Is(err, ErrCorruptRegistry) {
		t.Fatalf("err = %v, want ErrCorruptRegistry", err)
	}
	if r != nil {
		t.Error("a corrupt registry must not be returned for writing")
	}
	after, _ := os.ReadFile(path)
	if string(after) != junk {
		t.Errorf("corrupt file was modified: %q", after)
	}
}

func TestRegistryRefusesNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugins.json")
	const future = `{"schema": 99, "plugins": []}`
	_ = os.WriteFile(path, []byte(future), 0o600)
	if _, err := OpenRegistry(path); !errors.Is(err, ErrCorruptRegistry) {
		t.Fatalf("err = %v, want ErrCorruptRegistry for a newer schema", err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != future {
		t.Error("newer-schema file was rewritten (downgrade)")
	}
}

func TestRegistryListReturnsCopies(t *testing.T) {
	r, _ := OpenRegistry(filepath.Join(t.TempDir(), "plugins.json"))
	_ = r.Put(sampleEntry("alpha"))
	list := r.List()
	list[0].Permissions[0] = "tampered"
	list[0].Enabled = true
	got, _ := r.Get("alpha")
	if got.Permissions[0] != "network" || got.Enabled {
		t.Errorf("registry state was mutated through a returned copy: %+v", got)
	}
}

func TestRegistryAtomicWriteLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	r, _ := OpenRegistry(filepath.Join(dir, "plugins.json"))
	for _, id := range []string{"aaa", "bbb", "ccc"} {
		if err := r.Put(sampleEntry(id)); err != nil {
			t.Fatal(err)
		}
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 1 || files[0].Name() != "plugins.json" {
		t.Errorf("directory contains leftovers: %v", files)
	}
}

func TestRegistryRejectsInvalidArchiveBase(t *testing.T) {
	r, _ := OpenRegistry(filepath.Join(t.TempDir(), "plugins.json"))
	if err := r.SetArchiveBase("http://evil.example"); err == nil {
		t.Fatal("plain-http non-loopback base accepted")
	}
	if got := r.ArchiveBase(); got != "" {
		t.Errorf("ArchiveBase changed after a rejected value: %q", got)
	}
}

func TestRegistryPersistsMasterManagementAndTheManagedFlag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugins.json")
	r, err := OpenRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	if r.AllowMasterManagement() {
		t.Fatal("a master must not be able to manage plugins until the node owner allows it")
	}
	if err := r.SetAllowMasterManagement(true); err != nil {
		t.Fatal(err)
	}
	e := sampleEntry("alpha")
	e.ManagedByMaster = true
	if err := r.Put(e); err != nil {
		t.Fatal(err)
	}

	again, err := OpenRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := again.Get("alpha")
	if !again.AllowMasterManagement() || !got.ManagedByMaster {
		t.Errorf("allow=%v managed=%v after reopen", again.AllowMasterManagement(), got.ManagedByMaster)
	}
	if err := again.SetAllowMasterManagement(false); err != nil || again.AllowMasterManagement() {
		t.Errorf("revoking must stick: %v", err)
	}
}
