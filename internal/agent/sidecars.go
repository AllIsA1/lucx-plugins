// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/AllIsA1/lucx-plugins/internal/builds"
)

// Sidecars are helper programs the panel starts as processes (the OpenFlux exit, for one). The panel's own
// release carries a pinned build of each; the signed sidecars.json names a newer one, built by our CI from a
// reviewed upstream commit. The agent keeps the files in <panel dir>/bin in step with that list, the same way
// it does the panel binary: the timer has the file ready, the panel's start installs it, and the file it
// replaced is kept as <file>.orig so that restore can put it back.

const defaultSidecarsURL = "https://raw.githubusercontent.com/AllIsA1/lucx-plugins/builds/sidecars.json"

// SidecarState is what the agent knows about one sidecar it installed.
type SidecarState struct {
	Version     string    `json:"version"`
	SHA256      string    `json:"sha256"` // the file the agent installed
	InstalledAt time.Time `json:"installedAt"`
}

func (a *Agent) binDir() string {
	if a.cfg.BinDir != "" {
		return a.cfg.BinDir
	}
	return filepath.Join(filepath.Dir(a.cfg.PanelBinary), "bin")
}

func (a *Agent) sidecarsURL() string {
	if a.cfg.SidecarsURL != "" {
		return a.cfg.SidecarsURL
	}
	return defaultSidecarsURL
}

func (a *Agent) sidecarTarget(s builds.Sidecar) string {
	return filepath.Join(a.binDir(), s.Name+"-linux-"+s.Arch)
}

func (a *Agent) sidecarsCache() (index, sig string) {
	dir := filepath.Join(a.cfg.StateDir, "index")
	return filepath.Join(dir, "sidecars.json"), filepath.Join(dir, "sidecars.json.sig")
}

func (a *Agent) verifiedSidecars(data []byte, sig string) (*builds.SidecarIndex, error) {
	if err := builds.VerifySidecars(data, sig, a.cfg.Keys); err != nil {
		return nil, err
	}
	return builds.ParseSidecars(data)
}

func (a *Agent) cachedSidecars() (*builds.SidecarIndex, []byte) {
	indexPath, sigPath := a.sidecarsCache()
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, nil
	}
	sig, err := os.ReadFile(sigPath)
	if err != nil {
		return nil, nil
	}
	idx, err := a.verifiedSidecars(data, string(sig))
	if err != nil {
		a.cfg.Log("the cached sidecar list no longer verifies: %v", err)
		return nil, nil
	}
	return idx, data
}

// refreshSidecars returns the best verified sidecar list there is: a newer one from the network when it
// verifies and does not go backwards, otherwise the cached one. nil means none can be trusted (or none is
// published yet, which is not an error).
func (a *Agent) refreshSidecars(ctx context.Context, st *State) *builds.SidecarIndex {
	cached, cachedRaw := a.cachedSidecars()
	data, err := a.fetchSmall(ctx, a.sidecarsURL(), defaultMaxIndex)
	var sig []byte
	if err == nil {
		sig, err = a.fetchSmall(ctx, a.sidecarsURL()+".sig", 4096)
	}
	if err != nil {
		a.cfg.Log("cannot fetch the sidecar list (%v); using the cached one", err)
		return cached
	}
	fresh, err := a.verifiedSidecars(data, strings.TrimSpace(string(sig)))
	if err != nil {
		a.cfg.Log("the published sidecar list is refused: %v", err)
		st.SidecarError = "the published sidecar list is refused: " + err.Error()
		return cached
	}
	switch {
	case fresh.Sequence < st.SidecarsSequence:
		a.cfg.Log("the published sidecar list (sequence %d) is older than one already accepted (%d); ignoring it", fresh.Sequence, st.SidecarsSequence)
		st.SidecarError = "the published sidecar list went backwards and was ignored"
		return cached
	case fresh.Sequence == st.SidecarsSequence && cachedRaw != nil && !bytes.Equal(data, cachedRaw):
		a.cfg.Log("the published sidecar list has the sequence of the cached one but different content; ignoring it")
		st.SidecarError = "the published sidecar list changed without a new sequence and was ignored"
		return cached
	}
	indexPath, sigPath := a.sidecarsCache()
	if err := writeFileAtomic(indexPath, data, 0o644); err == nil {
		err = writeFileAtomic(sigPath, []byte(strings.TrimSpace(string(sig))+"\n"), 0o644)
		if err != nil {
			a.cfg.Log("cannot cache the sidecar list: %v", err)
		}
	} else {
		a.cfg.Log("cannot cache the sidecar list: %v", err)
	}
	st.SidecarsSequence = fresh.Sequence
	return fresh
}

func (a *Agent) sidecarCached(s builds.Sidecar) string {
	return filepath.Join(a.cfg.StateDir, "sidecars", s.Name+"-"+s.SHA256)
}

// syncSidecars brings the sidecar files in line with the signed list. Without install it only has each
// missing file ready and reports which ones a start would install; with install it installs them. A
// failure is logged and recorded in the state, never returned: a sidecar must not hold anything else up.
func (a *Agent) syncSidecars(ctx context.Context, st *State, install bool) (waiting []string) {
	if a.cfg.NoSidecars {
		return nil
	}
	st.SidecarError = ""
	if info, err := os.Stat(a.binDir()); err != nil || !info.IsDir() {
		return nil // not a panel layout we know: leave it alone
	}
	idx := a.refreshSidecars(ctx, st)
	if idx == nil {
		return nil
	}
	for _, s := range idx.Sidecars {
		if s.Arch != a.cfg.Arch {
			continue
		}
		target := a.sidecarTarget(s)
		have, _ := fileSha(target)
		if have == s.SHA256 {
			if st.Sidecars[s.Name].SHA256 != have {
				a.rememberSidecar(st, s)
			}
			continue
		}
		cached := a.sidecarCached(s)
		if got, err := fileSha(cached); err != nil || got != s.SHA256 {
			if err := os.MkdirAll(filepath.Dir(cached), 0o755); err != nil {
				st.SidecarError = err.Error()
				continue
			}
			if err := a.downloadVerified(ctx, s.URL, s.SHA256, cached); err != nil {
				a.cfg.Log("not installing %s %s: %v", s.Name, s.Version, err)
				st.SidecarError = fmt.Sprintf("%s %s: %v", s.Name, s.Version, err)
				continue
			}
		}
		if !install {
			waiting = append(waiting, s.Name)
			continue
		}
		if err := a.installSidecar(s, target, cached, have, st); err != nil {
			a.cfg.Log("not installing %s %s: %v", s.Name, s.Version, err)
			st.SidecarError = fmt.Sprintf("%s %s: %v", s.Name, s.Version, err)
			continue
		}
		a.cfg.Log("installed %s %s", s.Name, s.Version)
	}
	sort.Strings(waiting)
	return waiting
}

func (a *Agent) rememberSidecar(st *State, s builds.Sidecar) {
	if st.Sidecars == nil {
		st.Sidecars = map[string]SidecarState{}
	}
	st.Sidecars[s.Name] = SidecarState{Version: s.Version, SHA256: s.SHA256, InstalledAt: time.Now().UTC()}
}

var elfMagic = []byte{0x7f, 'E', 'L', 'F'}

func (a *Agent) installSidecar(s builds.Sidecar, target, cached, have string, st *State) error {
	head := make([]byte, len(elfMagic))
	f, err := os.Open(cached)
	if err != nil {
		return err
	}
	_, rerr := f.Read(head)
	_ = f.Close()
	if rerr != nil || !bytes.Equal(head, elfMagic) {
		return errors.New("the download is not an executable")
	}
	// Keep what was there, once, unless it is something this agent installed itself.
	if have != "" && st.Sidecars[s.Name].SHA256 != have {
		if _, err := os.Stat(target + ".orig"); errors.Is(err, os.ErrNotExist) {
			if err := copyFile(target, target+".orig"); err != nil {
				return fmt.Errorf("cannot keep the file it replaces: %w", err)
			}
		}
	}
	if err := replaceFile(cached, target); err != nil {
		return err
	}
	a.rememberSidecar(st, s)
	return nil
}

// restoreSidecars puts back the files the agent replaced, when the file in place is still the one it
// installed. It reports whether it changed anything.
func (a *Agent) restoreSidecars(st *State) bool {
	changed := false
	for name, rec := range st.Sidecars {
		target := filepath.Join(a.binDir(), name+"-linux-"+a.cfg.Arch)
		if now, err := fileSha(target); err != nil || now != rec.SHA256 {
			continue
		}
		orig := target + ".orig"
		if _, err := os.Stat(orig); err != nil {
			continue
		}
		if err := replaceFile(orig, target); err != nil {
			a.cfg.Log("cannot restore %s: %v", target, err)
			continue
		}
		_ = os.Remove(orig)
		delete(st.Sidecars, name)
		a.cfg.Log("restored the original %s", name)
		changed = true
	}
	return changed
}
