// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// UpdateResult tells an operator what Update found and did.
type UpdateResult struct {
	Before  string // the build installed when it started, "" for an upstream binary
	After   string // the build installed now, or the one that waits when Waiting
	Changed bool   // the panel was restarted onto a newer build
	Waiting bool   // a newer build is ready and will be installed at the panel's next start
	// Sidecars names the helper programs (OpenFlux...) that have a newer signed build: installed by the
	// restart when Changed, otherwise waiting for the next start.
	Sidecars []string
}

// Update is the manual way to get the newest build: it reads the signed list now, gets the build ready
// and, when restart is true, restarts the panel and checks that the restart installed it. Without a
// newer build nothing is restarted. A list that cannot be trusted, a binary that is not known and a
// restart that leaves the old build are errors, not silence.
func (a *Agent) Update(ctx context.Context, restart bool) (UpdateResult, error) {
	res, target, err := a.prepareUpdate(ctx)
	if err != nil || (target == nil && len(res.Sidecars) == 0) || !restart {
		return res, err
	}
	if target != nil {
		a.cfg.Log("restarting the panel to install %s", target.Release)
	} else {
		a.cfg.Log("restarting the panel to install %s", strings.Join(res.Sidecars, ", "))
	}
	if err := a.cfg.Restart(ctx); err != nil {
		return res, fmt.Errorf("cannot restart the panel: %w", err)
	}
	st := a.Status()
	if target != nil {
		now, err := fileSha(a.cfg.PanelBinary)
		if err != nil || now != target.BinarySha256 || st.Release != target.Release {
			reason := "the build is not installed"
			if st.LastError != "" {
				reason = st.LastError
			}
			return res, fmt.Errorf("the panel was restarted but %s was not installed: %s", target.Release, reason)
		}
		res.After, res.Changed, res.Waiting = target.Release, true, false
	}
	if len(res.Sidecars) > 0 {
		if left := a.sidecarsWaiting(res.Sidecars); len(left) > 0 {
			reason := st.SidecarError
			if reason == "" {
				reason = "the file is not installed"
			}
			return res, fmt.Errorf("the panel was restarted but %s was not installed: %s", strings.Join(left, ", "), reason)
		}
		res.Changed = true
	}
	return res, nil
}

// sidecarsWaiting says which of the named sidecars are still not the signed build on disk.
func (a *Agent) sidecarsWaiting(names []string) (left []string) {
	idx, _ := a.cachedSidecars()
	if idx == nil {
		return names
	}
	for _, name := range names {
		s, ok := idx.Find(name, a.cfg.Arch)
		if !ok {
			continue
		}
		if have, _ := fileSha(a.sidecarTarget(s)); have != s.SHA256 {
			left = append(left, name)
		}
	}
	return left
}

// prepareUpdate refreshes the list and, when a newer build exists, has it on disk. It holds the lock
// only while it works: the restart that follows runs at-start, which needs the lock too.
func (a *Agent) prepareUpdate(ctx context.Context) (UpdateResult, *buildRef, error) {
	unlock, err := a.lock(ctx)
	if err != nil {
		return UpdateResult{}, nil, err
	}
	defer unlock()
	st := a.Status()
	st.CheckedAt = time.Now().UTC()
	st.LastError = ""
	defer func() { _ = a.saveState(st) }()

	idx := a.refresh(ctx, &st)
	if st.LastError != "" {
		return UpdateResult{}, nil, errors.New(st.LastError)
	}
	if idx == nil {
		st.LastError = errNoVerifiedList.Error()
		return UpdateResult{}, nil, errNoVerifiedList
	}
	h, err := fileSha(a.cfg.PanelBinary)
	if err != nil {
		st.LastError = err.Error()
		return UpdateResult{}, nil, err
	}
	res := UpdateResult{}
	if st.Ours != "" && st.Ours == h {
		res.Before = st.Release
	}
	k, b := a.decide(idx, h, st)
	if k == none {
		if b == nil {
			err := errors.New("the panel binary is not a known original or plugin build; nothing to update")
			st.LastError = err.Error()
			return res, nil, err
		}
		st.Ours, st.Original, st.Upstream, st.Release, st.Pending, st.PendingRelease = b.BinarySha256, b.OriginalBinarySha256, b.Upstream, b.Release, false, ""
		res.Before, res.After = b.Release, b.Release
		if res.Sidecars = a.syncSidecars(ctx, &st, false); len(res.Sidecars) > 0 {
			st.Pending, st.PendingRelease, res.Waiting = true, strings.Join(res.Sidecars, ", "), true
		}
		return res, nil, nil
	}
	if _, err := a.prefetch(ctx, b); err != nil {
		st.LastError = err.Error()
		return res, nil, err
	}
	res.Sidecars = a.syncSidecars(ctx, &st, false)
	st.Pending, st.PendingRelease = true, b.Release
	res.After, res.Waiting = b.Release, true
	return res, &buildRef{Release: b.Release, BinarySha256: b.BinarySha256}, nil
}

// buildRef is what Update needs to know about the build it is moving to.
type buildRef struct{ Release, BinarySha256 string }
