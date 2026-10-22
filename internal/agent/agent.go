// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/AllIsA1/lucx-plugins/internal/builds"
)

func (a *Agent) builtPath(b *builds.Build) string {
	return filepath.Join(a.cfg.StateDir, "builds", b.BinarySha256, "x-ui")
}

func (a *Agent) originalPath(sha string) string {
	return filepath.Join(a.cfg.StateDir, "originals", sha, "x-ui")
}

// prefetch makes sure the verified build is on disk.
func (a *Agent) prefetch(ctx context.Context, b *builds.Build) (string, error) {
	dest := a.builtPath(b)
	if got, err := fileSha(dest); err == nil && got == b.BinarySha256 {
		return dest, nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	archive := dest + ".tar.gz"
	defer os.Remove(archive)
	if err := a.downloadVerified(ctx, b.Build.URL, b.Build.SHA256, archive); err != nil {
		return "", err
	}
	if err := a.extractBinary(archive, dest, b.BinarySha256); err != nil {
		return "", err
	}
	return dest, nil
}

// sanity runs the new binary once; a build that cannot even print its version is not installed.
func sanity(ctx context.Context, path, upstreamTag string) error {
	ctx, cancel := context.WithTimeout(ctx, sanityTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "-v").CombinedOutput()
	if err != nil {
		return fmt.Errorf("the new binary does not run: %w", err)
	}
	if want := upstreamTag[strings.LastIndex(upstreamTag, "-")+1:]; !strings.Contains(string(out), want) {
		return fmt.Errorf("the new binary reports %q, expected the release %s", strings.TrimSpace(string(out)), want)
	}
	return nil
}

// replaceFile atomically puts src in place of dest: same directory, same file system, then a rename.
func replaceFile(src, dest string) error {
	dir := filepath.Dir(dest)
	tmp := dest + ".lucx-new"
	_ = os.Remove(tmp)
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// swap installs build b over the panel binary whose hash is from.
func (a *Agent) swap(ctx context.Context, from string, b *builds.Build, st *State) error {
	built, err := a.prefetch(ctx, b)
	if err != nil {
		return err
	}
	if err := sanity(ctx, built, b.Upstream); err != nil {
		return err
	}
	// The panel may have been updated while the build was downloading; only replace what was recognised.
	if now, err := fileSha(a.cfg.PanelBinary); err != nil || now != from {
		return errors.New("the panel binary changed while the build was prepared; leaving it alone")
	}
	if kept := a.originalPath(b.OriginalBinarySha256); from == b.OriginalBinarySha256 {
		if got, err := fileSha(kept); err != nil || got != from {
			if err := os.MkdirAll(filepath.Dir(kept), 0o755); err != nil {
				return err
			}
			if err := copyFile(a.cfg.PanelBinary, kept); err != nil {
				return fmt.Errorf("cannot keep the original: %w", err)
			}
		}
	}
	if err := replaceFile(built, a.cfg.PanelBinary); err != nil {
		return err
	}
	st.Original, st.Ours, st.Upstream = b.OriginalBinarySha256, b.BinarySha256, b.Upstream
	st.SwappedAt = time.Now().UTC()
	st.Pending = false
	st.LastError = ""
	a.cfg.Log("replaced the panel binary with %s (build %s)", b.Upstream, b.Release)
	return nil
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dest + ".tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}

// AtStart is what runs before the panel starts. It never blocks the start for long (Config.Budget)
// and never makes it fail: the returned error is only for the log.
func (a *Agent) AtStart(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, a.cfg.Budget)
	defer cancel()
	unlock, err := a.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	st := a.Status()
	st.CheckedAt = time.Now().UTC()
	defer func() { _ = a.saveState(st) }()

	idx := a.refresh(ctx, &st)
	if idx == nil {
		if st.LastError == "" {
			st.LastError = errNoVerifiedList.Error()
		}
		return nil
	}
	h, err := fileSha(a.cfg.PanelBinary)
	if err != nil {
		st.LastError = err.Error()
		return err
	}
	k, b := a.decide(idx, h, st)
	if k == none {
		if b != nil { // already our current build
			st.Ours, st.Original, st.Upstream, st.Pending, st.LastError = b.BinarySha256, b.OriginalBinarySha256, b.Upstream, false, ""
		}
		return nil
	}
	if err := a.swap(ctx, h, b, &st); err != nil {
		st.LastError = err.Error()
		a.cfg.Log("not replacing the panel binary: %v", err)
		return err
	}
	return nil
}

// Check is the timer's job: refresh the list and have the build ready, without touching the
// binary of a panel that is running. It records whether a swap is waiting for the next start.
func (a *Agent) Check(ctx context.Context) error {
	unlock, err := a.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	st := a.Status()
	st.CheckedAt = time.Now().UTC()
	defer func() { _ = a.saveState(st) }()
	idx := a.refresh(ctx, &st)
	if idx == nil {
		return nil
	}
	h, err := fileSha(a.cfg.PanelBinary)
	if err != nil {
		st.LastError = err.Error()
		return err
	}
	k, b := a.decide(idx, h, st)
	st.Pending = false
	if k == none {
		return nil
	}
	if _, err := a.prefetch(ctx, b); err != nil {
		st.LastError = err.Error()
		return err
	}
	st.Pending, st.LastError = true, ""
	return nil
}

// Apply restarts the panel when a swap is waiting; the restart's own start does the swap.
func (a *Agent) Apply(ctx context.Context) error {
	if err := a.Check(ctx); err != nil {
		return err
	}
	if !a.Status().Pending {
		return nil
	}
	a.cfg.Log("restarting the panel to pick up the plugin build")
	return a.cfg.Restart(ctx)
}

// Restore puts the original panel binary back. It reports whether it changed anything; a binary
// that is not our build is left alone.
func (a *Agent) Restore(ctx context.Context) (bool, error) {
	unlock, err := a.lock(ctx)
	if err != nil {
		return false, err
	}
	defer unlock()
	st := a.Status()
	h, err := fileSha(a.cfg.PanelBinary)
	if err != nil {
		return false, err
	}
	idx, _ := a.cachedIndex()
	var b *builds.Build
	if idx != nil {
		if found, k := idx.Lookup(h, a.cfg.Arch); k == builds.Ours {
			b = found
		}
	}
	origSha := ""
	switch {
	case b != nil:
		origSha = b.OriginalBinarySha256
	case st.Ours != "" && st.Ours == h:
		origSha = st.Original
	default:
		return false, nil
	}
	src := a.originalPath(origSha)
	if got, err := fileSha(src); err != nil || got != origSha {
		src, err = a.fetchOriginal(ctx, idx, origSha)
		if err != nil {
			return false, err
		}
		defer os.Remove(src)
	}
	if err := replaceFile(src, a.cfg.PanelBinary); err != nil {
		return false, err
	}
	st.Original, st.Ours, st.Upstream, st.Pending, st.LastError = "", "", "", false, ""
	_ = a.saveState(st)
	a.cfg.Log("restored the original panel binary")
	return true, nil
}

// fetchOriginal downloads the upstream release the listed original came from.
func (a *Agent) fetchOriginal(ctx context.Context, idx *builds.Index, origSha string) (string, error) {
	if idx != nil {
		for i := range idx.Builds {
			b := &idx.Builds[i]
			if b.Arch == a.cfg.Arch && b.OriginalBinarySha256 == origSha {
				dir, err := os.MkdirTemp(a.cfg.StateDir, "restore-*")
				if err != nil {
					return "", err
				}
				defer os.RemoveAll(dir)
				archive := filepath.Join(dir, "original.tar.gz")
				if err := a.downloadVerified(ctx, b.OriginalArchive.URL, b.OriginalArchive.SHA256, archive); err != nil {
					return "", err
				}
				out := filepath.Join(a.cfg.StateDir, "restore-original")
				if err := a.extractBinary(archive, out, origSha); err != nil {
					return "", err
				}
				return out, nil
			}
		}
	}
	return "", errors.New("the saved original is gone and the signed list does not name where to download it from")
}
