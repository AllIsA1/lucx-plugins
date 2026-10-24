// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package agent

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// UpdateResult tells an operator what Update found and did.
type UpdateResult struct {
	Before  string // the build installed when it started, "" for an upstream binary
	After   string // the build installed now, or the one that waits when Waiting
	Changed bool   // the panel was restarted onto a newer build
	Waiting bool   // a newer build is ready and will be installed at the panel's next start
}

// Update is the manual way to get the newest build: it reads the signed list now, gets the build ready
// and, when restart is true, restarts the panel and checks that the restart installed it. Without a
// newer build nothing is restarted. A list that cannot be trusted, a binary that is not known and a
// restart that leaves the old build are errors, not silence.
func (a *Agent) Update(ctx context.Context, restart bool) (UpdateResult, error) {
	res, target, err := a.prepareUpdate(ctx)
	if err != nil || target == nil || !restart {
		return res, err
	}
	a.cfg.Log("restarting the panel to install %s", target.Release)
	if err := a.cfg.Restart(ctx); err != nil {
		return res, fmt.Errorf("cannot restart the panel: %w", err)
	}
	st := a.Status()
	now, err := fileSha(a.cfg.PanelBinary)
	if err != nil || now != target.BinarySha256 || st.Release != target.Release {
		reason := "the build is not installed"
		if st.LastError != "" {
			reason = st.LastError
		}
		return res, fmt.Errorf("the panel was restarted but %s was not installed: %s", target.Release, reason)
	}
	res.After, res.Changed, res.Waiting = target.Release, true, false
	return res, nil
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
		return res, nil, nil
	}
	if _, err := a.prefetch(ctx, b); err != nil {
		st.LastError = err.Error()
		return res, nil, err
	}
	st.Pending, st.PendingRelease = true, b.Release
	res.After, res.Waiting = b.Release, true
	return res, &buildRef{Release: b.Release, BinarySha256: b.BinarySha256}, nil
}

// buildRef is what Update needs to know about the build it is moving to.
type buildRef struct{ Release, BinarySha256 string }
