// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package agent

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// fetchSmall downloads a small document into memory.
func (a *Agent) fetchSmall(ctx context.Context, url string, limit int64) ([]byte, error) {
	resp, err := a.get(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", url, limit)
	}
	return raw, nil
}

func (a *Agent) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.cfg.Client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return resp, nil
}

// downloadVerified fetches a tarball into dest and refuses it unless it has the signed hash.
func (a *Agent) downloadVerified(ctx context.Context, url, wantSha, dest string) error {
	resp, err := a.get(ctx, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(resp.Body, a.cfg.MaxArchive+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n > a.cfg.MaxArchive {
		return fmt.Errorf("the archive is larger than the allowed %d bytes", a.cfg.MaxArchive)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != wantSha {
		return fmt.Errorf("the archive hash %s does not match the signed list (%s)", got, wantSha)
	}
	return nil
}

// extractBinary unpacks the one file an archive may contain, x-ui/x-ui, to dest, and refuses it
// unless it has the expected hash. Anything else in the archive is an error.
func (a *Agent) extractBinary(archive, dest, wantSha string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("not a gzip archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	found := false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("unreadable archive: %w", err)
		}
		switch {
		case hdr.Typeflag == tar.TypeDir && (hdr.Name == "x-ui" || hdr.Name == "x-ui/"):
			continue
		case hdr.Typeflag == tar.TypeReg && hdr.Name == "x-ui/x-ui":
			if found {
				return errors.New("the archive holds more than one x-ui/x-ui")
			}
			found = true
			if err := a.writeVerified(tr, dest, wantSha); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unexpected entry %q in the archive", hdr.Name)
		}
	}
	if !found {
		return errors.New("the archive has no x-ui/x-ui")
	}
	return nil
}

func (a *Agent) writeVerified(r io.Reader, dest, wantSha string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(r, a.cfg.MaxBinary+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(dest)
		return err
	}
	if n > a.cfg.MaxBinary {
		_ = os.Remove(dest)
		return fmt.Errorf("the binary is larger than the allowed %d bytes", a.cfg.MaxBinary)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != wantSha {
		_ = os.Remove(dest)
		return fmt.Errorf("the binary hash %s does not match the signed list (%s)", got, wantSha)
	}
	return nil
}

func fileSha(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
