// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
)

// archiveMembers names the program inside the release archive of each core whose project ships one (mieru's
// mita, TrustTunnel's endpoint). Cores that are not here take a plain binary only: the rest are builds of the panel's own.
var archiveMembers = map[string]string{
	"mieru":       "mita",
	"trusttunnel": "trusttunnel_endpoint",
}

// archiveMemberFor returns the program a core's release archive carries, from the core's binary file name
// ("mieru-linux-amd64" -> "mita"), or "" when the core takes no archive.
func archiveMemberFor(binaryPath string) string {
	base := filepath.Base(binaryPath)
	for core, member := range archiveMembers {
		if strings.HasPrefix(base, core+"-"+runtime.GOOS+"-") {
			return member
		}
	}
	return ""
}

var (
	gzipMagic = []byte{0x1f, 0x8b}
	elfMagic  = []byte{0x7f, 'E', 'L', 'F'}
)

func hasMagic(file string, magic []byte) bool {
	f, err := os.Open(file)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, len(magic))
	if _, err := io.ReadFull(f, head); err != nil {
		return false
	}
	return bytes.Equal(head, magic)
}

// unpackCore turns what was downloaded into the core's program at file. A gzip tar archive gives up the one member
// named member (a regular file, found by its base name wherever it sits); a plain program stays as it is. Either way
// the result must be an ELF binary, so a page or a text file can never be put where the panel runs a core as root.
// The caller has already checked the download against the SHA-256 it was promised.
func unpackCore(file, member string) error {
	if hasMagic(file, gzipMagic) {
		if member == "" {
			return common.NewError("tunnel: this core takes a plain binary, not an archive")
		}
		out := file + ".unpack"
		if err := extractMember(file, member, out); err != nil {
			_ = os.Remove(out)
			return err
		}
		if err := os.Rename(out, file); err != nil {
			_ = os.Remove(out)
			return err
		}
	}
	if !hasMagic(file, elfMagic) {
		return common.NewError("tunnel: the download is not an ELF binary")
	}
	return nil
}

func extractMember(archive, member, dst string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return common.NewErrorf("tunnel: the download is not a gzip archive: %v", err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return common.NewErrorf("tunnel: %s is not in the archive", member)
		}
		if err != nil {
			return common.NewErrorf("tunnel: the archive is broken: %v", err)
		}
		if hdr.Typeflag != tar.TypeReg || path.Base(hdr.Name) != member {
			continue
		}
		if hdr.Size > maxTunnelBinaryDownload {
			return common.NewError("tunnel: the program in the archive exceeds the size limit")
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		n, err := io.Copy(out, io.LimitReader(tr, maxTunnelBinaryDownload+1))
		if closeErr := out.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
		if n > maxTunnelBinaryDownload {
			return common.NewError("tunnel: the program in the archive exceeds the size limit")
		}
		return nil
	}
}
