// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var fakeELF = append([]byte{0x7f, 'E', 'L', 'F'}, bytes.Repeat([]byte{1}, 64)...)

func tarGz(t *testing.T, files map[string][]byte, order ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, name := range order {
		body := files[name]
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeTemp(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "download")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestArchiveMemberFollowsTheCoreFileName(t *testing.T) {
	for file, want := range map[string]string{
		"/x/bin/mieru-linux-amd64":        "mita",
		"/x/bin/trusttunnel-linux-arm64":  "trusttunnel_endpoint",
		"/x/bin/openflux-linux-amd64":     "",
		"/x/bin/anytls-linux-amd64":       "",
		"/x/bin/mieru-client-linux-amd64": "", // the client is a sidecar, never an archive here
	} {
		if got := archiveMemberFor(file); got != want {
			t.Errorf("archiveMemberFor(%s) = %q, want %q", file, got, want)
		}
	}
}

func TestUnpackCoreTakesTheProgramOutOfTheArchive(t *testing.T) {
	archive := tarGz(t, map[string][]byte{
		"trusttunnel-v1/trusttunnel_endpoint":     fakeELF,
		"trusttunnel-v1/trusttunnel_endpoint.sig": []byte("sig"),
		"trusttunnel-v1/setup_wizard":             []byte("\x7fELFwizard"),
	}, "trusttunnel-v1/setup_wizard", "trusttunnel-v1/trusttunnel_endpoint.sig", "trusttunnel-v1/trusttunnel_endpoint")
	file := writeTemp(t, archive)
	if err := unpackCore(file, "trusttunnel_endpoint"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(file)
	if !bytes.Equal(got, fakeELF) {
		t.Errorf("the file holds %d bytes, want the endpoint (%d)", len(got), len(fakeELF))
	}
	if _, err := os.Stat(file + ".unpack"); err == nil {
		t.Error("the temporary unpack file is left behind")
	}
}

func TestUnpackCoreKeepsAPlainBinary(t *testing.T) {
	file := writeTemp(t, fakeELF)
	if err := unpackCore(file, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(file); !bytes.Equal(got, fakeELF) {
		t.Error("a plain binary must stay as it is")
	}
}

func TestUnpackCoreRefusesWhatIsNotAProgram(t *testing.T) {
	cases := map[string]struct {
		data   []byte
		member string
		want   string
	}{
		"a page":                  {[]byte("<html>not found</html>"), "", "not an ELF"},
		"the member is missing":   {tarGz(t, map[string][]byte{"a/other": fakeELF}, "a/other"), "mita", "not in the archive"},
		"the member is text":      {tarGz(t, map[string][]byte{"mita": []byte("#!/bin/sh")}, "mita"), "mita", "not an ELF"},
		"an archive for a binary": {tarGz(t, map[string][]byte{"openflux": fakeELF}, "openflux"), "", "takes a plain binary"},
		"a broken archive":        {append([]byte{0x1f, 0x8b}, []byte("garbage")...), "mita", "not a gzip archive"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			err := unpackCore(writeTemp(t, c.data), c.member)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("got %v, want an error containing %q", err, c.want)
			}
		})
	}
}

func TestUnpackCoreIgnoresADirectoryOfTheSameName(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	_ = tw.WriteHeader(&tar.Header{Name: "mita", Typeflag: tar.TypeDir, Mode: 0o755})
	_ = tw.Close()
	_ = zw.Close()
	if err := unpackCore(writeTemp(t, buf.Bytes()), "mita"); err == nil || !strings.Contains(err.Error(), "not in the archive") {
		t.Errorf("got %v, want not in the archive", err)
	}
}
