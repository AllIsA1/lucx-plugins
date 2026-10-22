// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

//go:build linux

package plugins

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestExecArgsRoundTrip(t *testing.T) {
	want := execConfig{UID: 41000, GID: 41000, Parent: 77, DataBytes: 268435456, NProc: 64, NoFile: 4096, Command: "/srv/p/bin/hello", Args: []string{"--socket", "/run/s.sock", "--"}}
	got, err := parseExecArgs(execArgv(want))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
}

func TestParseExecArgsRejectsUnsafeOrBrokenInput(t *testing.T) {
	cases := map[string][]string{
		"no uid":        {"--gid", "5", "--parent", "1"},
		"root uid":      {"--uid", "0", "--gid", "5", "--parent", "1"},
		"root gid":      {"--uid", "5", "--gid", "0", "--parent", "1"},
		"no parent":     {"--uid", "5", "--gid", "5"},
		"not a number":  {"--uid", "x", "--gid", "5", "--parent", "1"},
		"negative":      {"--uid", "-5", "--gid", "5", "--parent", "1"},
		"unknown flag":  {"--uid", "5", "--gid", "5", "--parent", "1", "--shell", "yes"},
		"missing value": {"--uid", "5", "--gid", "5", "--parent"},
	}
	for name, args := range cases {
		if _, err := parseExecArgs(args); err == nil {
			t.Errorf("%s: parseExecArgs(%v) succeeded", name, args)
		}
	}
	got, err := parseExecArgs([]string{"--uid", "5", "--gid", "6", "--parent", "1"})
	if err != nil || got.Command != "" {
		t.Errorf("a self-check without a command must parse: %+v, %v", got, err)
	}
}

func TestParseMemoryLimit(t *testing.T) {
	cases := []struct {
		in   string
		want uint64
		bad  bool
	}{
		{"256M", 256 << 20, false},
		{"1G", 1 << 30, false},
		{"64K", 64 << 10, false},
		{"512", 512, false},
		{"infinity", 0, false},
		{"12X", 0, true},
		{"M", 0, true},
		{"-1M", 0, true},
	}
	for _, c := range cases {
		got, err := parseMemoryLimit(c.in)
		if (err != nil) != c.bad || got != c.want {
			t.Errorf("parseMemoryLimit(%q) = %d, %v; want %d, bad=%v", c.in, got, err, c.want, c.bad)
		}
	}
}

func TestUIDMapAllocatesStableDistinctUsers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uids.json")
	m := newUIDMap(path)
	a, err := m.For("alpha")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := m.For("bravo")
	if a != uidBase || b != uidBase+1 {
		t.Errorf("uids = %d, %d, want the first two of the range", a, b)
	}
	if again, _ := m.For("alpha"); again != a {
		t.Errorf("alpha moved from %d to %d", a, again)
	}
	reloaded := newUIDMap(path)
	if got, _ := reloaded.For("bravo"); got != b {
		t.Errorf("bravo = %d after reload, want %d", got, b)
	}
	if c, _ := reloaded.For("charlie"); c != uidBase+2 {
		t.Errorf("charlie = %d, want %d", c, uidBase+2)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("uid file: %v, %v; want mode 0600", st, err)
	}
}

func TestUIDMapRefusesToGuessWhenTheFileIsDamaged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uids.json")
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newUIDMap(path).For("alpha"); err == nil {
		t.Fatal("a damaged uid file was silently replaced; two plugins could end up sharing a user")
	}
}

func TestUIDMapRunsOutOfUsers(t *testing.T) {
	m := newUIDMap(filepath.Join(t.TempDir(), "uids.json"))
	m.limit = uidBase + 1
	for _, id := range []string{"alpha", "bravo"} {
		if _, err := m.For(id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.For("charlie"); err == nil || !strings.Contains(err.Error(), "no free") {
		t.Errorf("third plugin: %v, want the range-exhausted error", err)
	}
}

func TestCheckSearchableNamesTheClosedDirectory(t *testing.T) {
	root, err := os.MkdirTemp("", "searchable")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	closed := filepath.Join(root, "closed")
	open := filepath.Join(closed, "open")
	if err := os.MkdirAll(open, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(closed, 0o700); err != nil {
		t.Fatal(err)
	}
	err = checkSearchable(open)
	if err == nil || !strings.Contains(err.Error(), closed) {
		t.Errorf("checkSearchable = %v, want an error naming %s", err, closed)
	}
	if err := os.Chmod(closed, 0o711); err != nil {
		t.Fatal(err)
	}
	if err := checkSearchable(open); err != nil {
		t.Errorf("a searchable path was refused: %v", err)
	}
}
