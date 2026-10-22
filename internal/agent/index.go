// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package agent

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/AllIsA1/lucx-plugins/internal/builds"
)

func (a *Agent) indexCache() (index, sig string) {
	dir := filepath.Join(a.cfg.StateDir, "index")
	return filepath.Join(dir, "builds.json"), filepath.Join(dir, "builds.json.sig")
}

// verified parses a list only after its signature checks out.
func (a *Agent) verified(data []byte, sig string) (*builds.Index, error) {
	if err := builds.Verify(data, sig, a.cfg.Keys); err != nil {
		return nil, err
	}
	return builds.Parse(data)
}

func (a *Agent) cachedIndex() (*builds.Index, []byte) {
	indexPath, sigPath := a.indexCache()
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, nil
	}
	sig, err := os.ReadFile(sigPath)
	if err != nil {
		return nil, nil
	}
	idx, err := a.verified(data, string(sig))
	if err != nil {
		a.cfg.Log("the cached builds list no longer verifies: %v", err)
		return nil, nil
	}
	return idx, data
}

// refresh returns the best verified list there is: a newer one from the network when it verifies
// and does not go backwards, otherwise the cached one. nil means no list can be trusted.
func (a *Agent) refresh(ctx context.Context, st *State) *builds.Index {
	cached, cachedRaw := a.cachedIndex()
	data, err := a.fetchSmall(ctx, a.cfg.IndexURL, defaultMaxIndex)
	var sig []byte
	if err == nil {
		sig, err = a.fetchSmall(ctx, a.cfg.IndexURL+".sig", 4096)
	}
	if err != nil {
		a.cfg.Log("cannot fetch the builds list (%v); using the cached one", err)
		return cached
	}
	fresh, err := a.verified(data, strings.TrimSpace(string(sig)))
	if err != nil {
		a.cfg.Log("the published builds list is refused: %v", err)
		st.LastError = "the published builds list is refused: " + err.Error()
		return cached
	}
	switch {
	case fresh.Sequence < st.MaxSequence:
		a.cfg.Log("the published list (sequence %d) is older than one already accepted (%d); ignoring it", fresh.Sequence, st.MaxSequence)
		st.LastError = "the published builds list went backwards and was ignored"
		return cached
	case fresh.Sequence == st.MaxSequence && cachedRaw != nil && !bytes.Equal(data, cachedRaw):
		a.cfg.Log("the published list has the sequence of the cached one but different content; ignoring it")
		st.LastError = "the published builds list changed without a new sequence and was ignored"
		return cached
	}
	indexPath, sigPath := a.indexCache()
	if err := writeFileAtomic(indexPath, data, 0o644); err == nil {
		err = writeFileAtomic(sigPath, []byte(strings.TrimSpace(string(sig))+"\n"), 0o644)
		if err != nil {
			a.cfg.Log("cannot cache the builds list: %v", err)
		}
	} else {
		a.cfg.Log("cannot cache the builds list: %v", err)
	}
	st.MaxSequence = fresh.Sequence
	return fresh
}

type kind int

const (
	none     kind = iota // nothing to do
	original             // an upstream binary with a build available
	outdated             // our build, but the list has a newer build of the same release
)

// decide says what the binary with hash h calls for.
func (a *Agent) decide(idx *builds.Index, h string, st State) (kind, *builds.Build) {
	b, k := idx.Lookup(h, a.cfg.Arch)
	switch k {
	case builds.Original:
		return original, b
	case builds.Ours:
		return none, b
	}
	if st.Ours != "" && st.Ours == h && st.Original != "" {
		for i := range idx.Builds {
			nb := &idx.Builds[i]
			if nb.Arch == a.cfg.Arch && nb.OriginalBinarySha256 == st.Original && nb.BinarySha256 != h {
				return outdated, nb
			}
		}
	}
	return none, nil
}
