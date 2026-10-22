// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// uiPluginEnv installs a ui plugin whose archive carries files and returns its id.
func uiPluginEnv(t *testing.T, files map[string]string, mutate func(m map[string]any)) (*testEnv, string) {
	t.Helper()
	entries := []tarEntry{
		dir("page-0123456/"),
		file("page-0123456/lucx-plugin.json", string(manifestJSON(t, func(m map[string]any) {
			m["id"] = "page-a"
			m["type"] = "ui"
			delete(m, "run")
			m["permissions"] = []string{"ui:menu"}
			m["ui"] = map[string]any{"menu": []map[string]any{{"title": "Page", "path": "/ui/index.html"}}}
			if mutate != nil {
				mutate(m)
			}
		}))),
	}
	for name, body := range files {
		entries = append(entries, file("page-0123456/"+name, body))
	}
	env := newEnv(t, map[string][]byte{"/acme/alerts/tar.gz/HEAD": buildArchive(t, testCommit, entries...)})
	p, err := env.m.Preview(context.Background(), alertsURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.m.Install(p.Token, []string{"ui:menu"}); err != nil {
		t.Fatal(err)
	}
	if err := env.m.SetEnabled("page-a", true); err != nil {
		t.Fatal(err)
	}
	return env, "page-a"
}

func serveUI(env *testEnv, method, id, sub string) (*httptest.ResponseRecorder, error) {
	w := httptest.NewRecorder()
	err := env.m.ServeUI(w, httptest.NewRequest(method, "/x", nil), id, sub)
	return w, err
}

var pageFiles = map[string]string{
	"ui/index.html":                 "<h1>home</h1>",
	"ui/app.js":                     "console.log(1)",
	"ui/style.css":                  "body{}",
	"ui/data.bin1":                  "xyz",
	"ui/replace/clients/index.html": "<h1>clients</h1>",
	"ui/sub/readme.txt":             "no index here",
}

func TestServeUISendsTheFileInsideTheSandbox(t *testing.T) {
	env, id := uiPluginEnv(t, pageFiles, nil)
	tests := []struct{ path, body, typ string }{
		{"/ui/index.html", "<h1>home</h1>", "text/html"},
		{"/ui/app.js", "console.log(1)", "text/javascript"},
		{"/ui/style.css", "body{}", "text/css"},
		{"/ui/data.bin1", "xyz", "application/octet-stream"},
		{"/ui/", "<h1>home</h1>", "text/html"},
		{"/ui/replace/clients/", "<h1>clients</h1>", "text/html"},
	}
	for _, tc := range tests {
		w, err := serveUI(env, http.MethodGet, id, tc.path)
		if err != nil {
			t.Fatalf("%s: %v", tc.path, err)
		}
		if w.Body.String() != tc.body || !strings.HasPrefix(w.Header().Get("Content-Type"), tc.typ) {
			t.Errorf("%s: body %q type %q, want %q %q", tc.path, w.Body.String(), w.Header().Get("Content-Type"), tc.body, tc.typ)
		}
		if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox allow-scripts") || !strings.Contains(csp, "connect-src 'none'") {
			t.Errorf("%s: CSP = %q", tc.path, csp)
		}
		if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("X-Frame-Options") != "SAMEORIGIN" {
			t.Errorf("%s: headers = %v", tc.path, w.Header())
		}
	}
	if w, err := serveUI(env, http.MethodHead, id, "/ui/index.html"); err != nil || w.Body.Len() != 0 || w.Code != http.StatusOK {
		t.Errorf("HEAD = %d %q %v", w.Code, w.Body.String(), err)
	}
}

func TestServeUIRefusesWhatIsNotAPluginPage(t *testing.T) {
	env, id := uiPluginEnv(t, pageFiles, nil)
	outside := filepath.Join(env.codeDir, "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(env.codeDir, id, "ui", "leak.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(env.codeDir, filepath.Join(env.codeDir, id, "ui", "up")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		"/lucx-plugin.json", "/ui", "/bin/amd64/x", "/ui/../lucx-plugin.json", "/ui/../../secret.txt",
		"/index.html", "/app.js", "/ui/./index.html", "/ui/replace/../index.html", "/ui/sub/", "/ui/missing.html", "/ui/leak.txt", "/ui/up/secret.txt", "/ui//etc/passwd", "ui/index.html",
	} {
		if w, err := serveUI(env, http.MethodGet, id, p); !errors.Is(err, ErrNotFound) || w.Body.Len() != 0 {
			t.Errorf("%q: err = %v body %q, want ErrNotFound and nothing written", p, err, w.Body.String())
		}
	}
	if _, err := serveUI(env, http.MethodPost, id, "/ui/index.html"); !errors.Is(err, ErrMethodNotAllowed) {
		t.Errorf("POST = %v, want ErrMethodNotAllowed", err)
	}
}

func TestServeUIOnlyServesEnabledPluginsOfThisRole(t *testing.T) {
	env, id := uiPluginEnv(t, pageFiles, nil)
	for _, bad := range []string{"nope", "../page-a", "", ".staging"} {
		if _, err := serveUI(env, http.MethodGet, bad, "/ui/index.html"); !errors.Is(err, ErrNotFound) {
			t.Errorf("id %q: err = %v, want ErrNotFound", bad, err)
		}
	}
	if err := env.m.SetEnabled(id, false); err != nil {
		t.Fatal(err)
	}
	if w, err := serveUI(env, http.MethodGet, id, "/ui/index.html"); !errors.Is(err, ErrNotRunning) || w.Body.Len() != 0 {
		t.Errorf("disabled plugin: err = %v, want ErrNotRunning", err)
	}

	nodeOnly, nodeID := uiPluginEnv(t, pageFiles, func(m map[string]any) { m["scope"] = "node" })
	if _, err := serveUI(nodeOnly, http.MethodGet, nodeID, "/ui/index.html"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a node-only plugin on a master: err = %v, want ErrNotFound", err)
	}
}

func TestServeUIIsForStaticPluginsOnly(t *testing.T) {
	env := newEnv(t, map[string][]byte{"/acme/alerts/tar.gz/HEAD": goodArchive(t, nil)})
	p, _ := env.m.Preview(context.Background(), alertsURL)
	if _, err := env.m.Install(p.Token, defaultPerms()); err != nil {
		t.Fatal(err)
	}
	_ = env.m.SetEnabled("traffic-alerts", true)
	if _, err := serveUI(env, http.MethodGet, "traffic-alerts", "/ui/index.html"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a process plugin's pages come from its process, not from disk: err = %v", err)
	}
}
