// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package main

import (
	"embed"
	"mime"
	"net/http"
	"path"
	"strings"
)

// The page is part of the binary, so the plugin needs no file access beyond its own state directory.
//
//go:embed ui
var pages embed.FS

// pageHandler answers GET /ui/... with the embedded files; a path ending in / serves its index.html.
func pageHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		if strings.HasSuffix(name, "/") {
			name += "index.html"
		}
		data, err := pages.ReadFile(path.Clean(name))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		typ := mime.TypeByExtension(path.Ext(name))
		if typ == "" {
			typ = "application/octet-stream"
		}
		w.Header().Set("Content-Type", typ)
		_, _ = w.Write(data)
	})
}
