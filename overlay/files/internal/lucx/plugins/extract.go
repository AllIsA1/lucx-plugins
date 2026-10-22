// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var errTooLarge = errors.New("too large")

// Limits bound what a plugin archive may download and unpack to.
type Limits struct {
	MaxArchiveBytes int64
	MaxTotalBytes   int64
	MaxFileBytes    int64
	MaxFiles        int
	MaxDepth        int
	MaxPathLen      int
}

// DefaultLimits suit a plugin with a few binaries and a small web UI.
var DefaultLimits = Limits{
	MaxArchiveBytes: 100 << 20,
	MaxTotalBytes:   300 << 20,
	MaxFileBytes:    150 << 20,
	MaxFiles:        5000,
	MaxDepth:        12,
	MaxPathLen:      240,
}

// capReader fails with errTooLarge once more than left bytes would be read.
type capReader struct {
	r    io.Reader
	left int64
}

func (c *capReader) Read(p []byte) (int, error) {
	if c.left <= 0 {
		var probe [1]byte
		n, err := c.r.Read(probe[:])
		if n > 0 {
			return 0, errTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	return n, err
}

// ExtractTarGz unpacks a GitHub-style tarball into the existing directory dst,
// stripping the single root directory. It returns the commit id from the pax
// global header, or "" when the archive does not carry one.
func ExtractTarGz(r io.Reader, dst string, lim Limits) (string, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return "", fmt.Errorf("archive is not gzip: %w", err)
	}
	defer gz.Close()

	overhead := int64(lim.MaxFiles)*2*1536 + 1<<20
	tr := tar.NewReader(&capReader{r: gz, left: lim.MaxTotalBytes + overhead})

	root, err := os.OpenRoot(dst)
	if err != nil {
		return "", err
	}
	defer root.Close()

	ex := &extractor{root: root, tr: tr, lim: lim}
	commit, err := ex.run()
	if errors.Is(err, errTooLarge) {
		return "", fmt.Errorf("archive is too large when unpacked (limit %d bytes)", lim.MaxTotalBytes)
	}
	if err != nil {
		return "", err
	}
	if err := chmodDirs(dst); err != nil {
		return "", err
	}
	return commit, nil
}

type extractor struct {
	root  *os.Root
	tr    *tar.Reader
	lim   Limits
	top   string
	files int
	dirs  int
	total int64
}

func (e *extractor) run() (string, error) {
	commit := ""
	for {
		hdr, err := e.tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		if hdr.Typeflag == tar.TypeXGlobalHeader {
			if c := strings.ToLower(hdr.PAXRecords["comment"]); shaPattern.MatchString(c) {
				commit = c
			}
			continue
		}
		if err := e.entry(hdr); err != nil {
			return "", err
		}
	}
	if e.files == 0 {
		return "", errors.New("archive contains no files")
	}
	return commit, nil
}

func (e *extractor) entry(hdr *tar.Header) error {
	rel, isRoot, err := e.relPath(hdr.Name)
	if err != nil {
		return err
	}
	switch hdr.Typeflag {
	case tar.TypeDir:
		if isRoot {
			return nil
		}
		if e.dirs++; e.dirs > e.lim.MaxFiles {
			return errors.New("archive has too many directories")
		}
		return e.root.MkdirAll(rel, 0o755)
	case tar.TypeReg:
		if isRoot {
			return errors.New("archive must have a single root directory, found a loose file")
		}
		return e.file(hdr, rel)
	default:
		return fmt.Errorf("unsupported entry type %q for %q", string(hdr.Typeflag), hdr.Name)
	}
}

func (e *extractor) relPath(name string) (rel string, isRoot bool, err error) {
	if name == "" || strings.ContainsAny(name, "\x00\\:") || strings.HasPrefix(name, "/") {
		return "", false, fmt.Errorf("unsafe path %q in archive", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", false, fmt.Errorf("unsafe path %q in archive", name)
		}
	}
	parts := strings.Split(strings.Trim(path.Clean(name), "/"), "/")
	if e.top == "" {
		e.top = parts[0]
	} else if parts[0] != e.top {
		return "", false, errors.New("archive must have a single root directory")
	}
	if len(parts) == 1 {
		return "", true, nil
	}
	rel = strings.Join(parts[1:], "/")
	if !fs.ValidPath(rel) {
		return "", false, fmt.Errorf("unsafe path %q in archive", name)
	}
	if len(parts)-1 > e.lim.MaxDepth {
		return "", false, fmt.Errorf("path %q is too deep", name)
	}
	if len(rel) > e.lim.MaxPathLen {
		return "", false, fmt.Errorf("path %q is too long", name)
	}
	return rel, false, nil
}

func (e *extractor) file(hdr *tar.Header, rel string) error {
	if e.files++; e.files > e.lim.MaxFiles {
		return errors.New("archive has too many files")
	}
	if hdr.Size > e.lim.MaxFileBytes {
		return fmt.Errorf("file %q is too large", hdr.Name)
	}
	if e.total += hdr.Size; e.total > e.lim.MaxTotalBytes {
		return errTooLarge
	}
	if err := e.root.MkdirAll(path.Dir(rel), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if hdr.Mode&0o111 != 0 {
		mode = 0o755
	}
	f, err := e.root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("duplicate entry %q in archive", hdr.Name)
	}
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, e.tr)
	chmodErr := f.Chmod(mode)
	closeErr := f.Close()
	return errors.Join(copyErr, chmodErr, closeErr)
}

// chmodDirs makes directories world-readable regardless of the panel's umask
// so that unprivileged plugin processes can traverse them.
func chmodDirs(dst string) error {
	return filepath.WalkDir(dst, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.Chmod(p, 0o755)
		}
		return nil
	})
}
