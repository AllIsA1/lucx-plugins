// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"errors"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ErrMethodNotAllowed is returned for anything but GET and HEAD on a static page.
var ErrMethodNotAllowed = errors.New("only GET and HEAD are served")

// ServeUI answers a request for a page of an interface plugin (type ui) straight from its ui/
// directory. Nothing outside ui/ is reachable, nothing is listed and symlinks cannot leave it.
func (m *Manager) ServeUI(w http.ResponseWriter, r *http.Request, id, subpath string) error {
	e, ok := m.reg.Get(id)
	if !idPattern.MatchString(id) || !ok || e.Type != TypeUI || !runsOnThisPanel(e) {
		return ErrNotFound
	}
	if !e.Enabled {
		return ErrNotRunning
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return ErrMethodNotAllowed
	}
	rel, slash, ok := uiRelativePath(subpath)
	if !ok {
		return ErrNotFound
	}
	root, err := os.OpenRoot(filepath.Join(m.cfg.CodeDir, id, "ui"))
	if err != nil {
		return ErrNotFound
	}
	defer root.Close()

	info, err := root.Stat(rel)
	if err != nil {
		return ErrNotFound
	}
	if info.IsDir() {
		if !slash {
			return ErrNotFound
		}
		rel = path.Join(rel, "index.html")
	} else if slash {
		return ErrNotFound
	}
	f, err := root.Open(rel)
	if err != nil {
		return ErrNotFound
	}
	defer f.Close()
	if info, err = f.Stat(); err != nil || !info.Mode().IsRegular() {
		return ErrNotFound
	}

	SetPageHeaders(w.Header())
	w.Header().Set("Cache-Control", "no-cache")
	typ := mime.TypeByExtension(strings.ToLower(path.Ext(rel)))
	if typ == "" {
		typ = "application/octet-stream"
	}
	w.Header().Set("Content-Type", typ)
	http.ServeContent(w, r, "", info.ModTime(), f)
	return nil
}

// uiRelativePath turns /ui/a/b into a/b below the ui directory; only clean paths pass.
func uiRelativePath(subpath string) (rel string, trailingSlash, ok bool) {
	if !strings.HasPrefix(subpath, "/ui/") || strings.ContainsAny(subpath, "\\\x00") {
		return "", false, false
	}
	trailingSlash = strings.HasSuffix(subpath, "/")
	trimmed := strings.TrimSuffix(subpath, "/")
	if path.Clean(trimmed) != trimmed {
		return "", false, false
	}
	rel = strings.TrimPrefix(strings.TrimPrefix(trimmed, "/ui"), "/")
	if rel == "" {
		rel = "."
	}
	return rel, trailingSlash, true
}

// IsPagePath reports whether subpath is a clean path under /ui/, the only part of a plugin a page token opens.
func IsPagePath(subpath string) bool {
	_, _, ok := uiRelativePath(subpath)
	return ok
}
