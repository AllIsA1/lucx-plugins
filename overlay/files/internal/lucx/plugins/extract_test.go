// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testLimits() Limits {
	return Limits{MaxArchiveBytes: 1 << 20, MaxTotalBytes: 1 << 16, MaxFileBytes: 1 << 14, MaxFiles: 20, MaxDepth: 6, MaxPathLen: 120}
}

func TestExtractStripsRootAndNormalizesModes(t *testing.T) {
	dst := t.TempDir()
	data := buildArchive(t, testCommit,
		dir("repo-abc/"),
		file("repo-abc/lucx-plugin.json", "{}"),
		dir("repo-abc/ui/"),
		file("repo-abc/ui/index.html", "<html>"),
		tarEntry{name: "repo-abc/bin/amd64/alerts", typ: tar.TypeReg, body: "ELF", mode: 0o700},
		tarEntry{name: "repo-abc/secret.txt", typ: tar.TypeReg, body: "x", mode: 0o666},
	)
	commit, err := ExtractTarGz(bytes.NewReader(data), dst, testLimits())
	if err != nil {
		t.Fatalf("ExtractTarGz: %v", err)
	}
	if commit != testCommit {
		t.Errorf("commit = %q, want %q", commit, testCommit)
	}
	body, err := os.ReadFile(filepath.Join(dst, "ui", "index.html"))
	if err != nil || string(body) != "<html>" {
		t.Fatalf("ui/index.html = %q, %v", body, err)
	}
	wantModes := map[string]os.FileMode{
		"lucx-plugin.json": 0o644,
		"bin/amd64/alerts": 0o755,
		"secret.txt":       0o644,
		"ui":               0o755,
		"bin":              0o755,
	}
	for rel, want := range wantModes {
		st, err := os.Stat(filepath.Join(dst, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("stat %s: %v", rel, err)
			continue
		}
		if got := st.Mode().Perm(); got != want {
			t.Errorf("mode of %s = %v, want %v", rel, got, want)
		}
	}
}

func TestExtractReturnsEmptyCommitWithoutGlobalHeader(t *testing.T) {
	dst := t.TempDir()
	data := buildArchive(t, "", file("repo-abc/a.txt", "a"))
	commit, err := ExtractTarGz(bytes.NewReader(data), dst, testLimits())
	if err != nil {
		t.Fatalf("ExtractTarGz: %v", err)
	}
	if commit != "" {
		t.Errorf("commit = %q, want empty", commit)
	}
}

func TestExtractIgnoresMalformedCommitComment(t *testing.T) {
	dst := t.TempDir()
	data := buildArchive(t, "not-a-sha", file("repo-abc/a.txt", "a"))
	commit, err := ExtractTarGz(bytes.NewReader(data), dst, testLimits())
	if err != nil {
		t.Fatalf("ExtractTarGz: %v", err)
	}
	if commit != "" {
		t.Errorf("commit = %q, want empty for a malformed comment", commit)
	}
}

func TestExtractRejectsHostileEntries(t *testing.T) {
	cases := []struct {
		name    string
		entries []tarEntry
		want    string
	}{
		{"parent traversal", []tarEntry{file("repo/../../evil", "x")}, "unsafe path"},
		{"deep parent traversal", []tarEntry{file("repo/a/../../../evil", "x")}, "unsafe path"},
		{"absolute path", []tarEntry{file("/etc/cron.d/evil", "x")}, "unsafe path"},
		{"backslash", []tarEntry{file("repo/a\\b", "x")}, "unsafe path"},
		{"colon", []tarEntry{file("repo/C:evil", "x")}, "unsafe path"},
		{"symlink", []tarEntry{{name: "repo/link", typ: tar.TypeSymlink, link: "/etc/passwd"}}, "unsupported entry"},
		{"hardlink", []tarEntry{file("repo/a", "x"), {name: "repo/b", typ: tar.TypeLink, link: "repo/a"}}, "unsupported entry"},
		{"char device", []tarEntry{{name: "repo/null", typ: tar.TypeChar}}, "unsupported entry"},
		{"fifo", []tarEntry{{name: "repo/pipe", typ: tar.TypeFifo}}, "unsupported entry"},
		{"duplicate file", []tarEntry{file("repo/a", "1"), file("repo/a", "2")}, "duplicate"},
		{"two roots", []tarEntry{file("one/a", "1"), file("two/b", "2")}, "single root"},
		{"file at archive root", []tarEntry{file("loose.txt", "x")}, "single root"},
		{"file replaces dir", []tarEntry{file("repo/a/b", "1"), file("repo/a", "2")}, "duplicate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dst := filepath.Join(t.TempDir(), "out")
			if err := os.Mkdir(dst, 0o700); err != nil {
				t.Fatal(err)
			}
			data := buildArchive(t, testCommit, tc.entries...)
			_, err := ExtractTarGz(bytes.NewReader(data), dst, testLimits())
			if err == nil {
				t.Fatal("ExtractTarGz accepted a hostile archive")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
			assertNothingOutside(t, dst)
		})
	}
}

func assertNothingOutside(t *testing.T, dst string) {
	t.Helper()
	parent := filepath.Dir(dst)
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatalf("read %s: %v", parent, err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(dst) {
		t.Errorf("extraction wrote outside its destination: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(parent), "evil")); err == nil {
		t.Error("hostile entry escaped two levels up")
	}
}

func TestExtractEnforcesLimits(t *testing.T) {
	big := strings.Repeat("a", 1<<14+1)
	cases := []struct {
		name    string
		limits  func(l *Limits)
		entries []tarEntry
		want    string
	}{
		{"too many files", func(l *Limits) { l.MaxFiles = 3 }, []tarEntry{
			file("r/1", "x"), file("r/2", "x"), file("r/3", "x"), file("r/4", "x"),
		}, "too many files"},
		{"file too large", nil, []tarEntry{file("r/big", big)}, "too large"},
		{"total too large", func(l *Limits) { l.MaxTotalBytes = 100; l.MaxFileBytes = 80 }, []tarEntry{
			file("r/1", strings.Repeat("a", 60)), file("r/2", strings.Repeat("a", 60)),
		}, "too large"},
		{"path too deep", func(l *Limits) { l.MaxDepth = 3 }, []tarEntry{file("r/a/b/c/d", "x")}, "too deep"},
		{"path too long", func(l *Limits) { l.MaxPathLen = 20 }, []tarEntry{file("r/"+strings.Repeat("a", 30), "x")}, "too long"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lim := testLimits()
			if tc.limits != nil {
				tc.limits(&lim)
			}
			_, err := ExtractTarGz(bytes.NewReader(buildArchive(t, testCommit, tc.entries...)), t.TempDir(), lim)
			if err == nil {
				t.Fatal("ExtractTarGz ignored a limit")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestExtractRejectsDecompressionBomb(t *testing.T) {
	lim := testLimits()
	lim.MaxTotalBytes = 1 << 12
	lim.MaxFileBytes = 1 << 30
	body := strings.Repeat("\x00", 1<<20)
	data := buildArchive(t, testCommit, file("r/zeros", body))
	if len(data) > 8<<10 {
		t.Fatalf("test archive is %d bytes; it must compress well to model a bomb", len(data))
	}
	_, err := ExtractTarGz(bytes.NewReader(data), t.TempDir(), lim)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("err = %v, want a size error", err)
	}
}

func TestExtractRejectsNonGzip(t *testing.T) {
	if _, err := ExtractTarGz(strings.NewReader("this is not gzip"), t.TempDir(), testLimits()); err == nil {
		t.Fatal("ExtractTarGz accepted non-gzip input")
	}
}

func TestExtractRejectsEmptyArchive(t *testing.T) {
	data := buildArchive(t, testCommit)
	if _, err := ExtractTarGz(bytes.NewReader(data), t.TempDir(), testLimits()); err == nil {
		t.Fatal("ExtractTarGz accepted an archive with no files")
	}
}
