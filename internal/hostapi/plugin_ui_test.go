// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostapi

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AllIsA1/lucx-plugins/internal/plugins"
)

// newUIManager serves a static interface plugin that adds a menu entry and replaces /clients.
func newUIManager(t *testing.T) *plugins.Manager {
	t.Helper()
	manifest := `{"id":"page-a","name":"Page A","version":"1.0.0","apiVersion":"1","type":"ui",` +
		`"permissions":["ui:menu","ui:replace:/clients"],"ui":{"menu":[{"title":"Page A","icon":"bell","path":"/ui/index.html"}],"replaces":["/clients"]}}`
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Typeflag: tar.TypeXGlobalHeader, Name: "pax_global_header", PAXRecords: map[string]string{"comment": pluginsTestCommit}})
	for name, body := range map[string]string{
		"page-0123456/lucx-plugin.json":              manifest,
		"page-0123456/ui/index.html":                 "<h1>page a</h1>",
		"page-0123456/ui/replace/clients/index.html": "<h1>clients</h1>",
		"page-0123456/notes.txt":                     "not a page",
	} {
		_ = tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))})
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = gz.Close()
	archive := buf.Bytes()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/acme/page/tar.gz/HEAD" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(archive)
	}))
	t.Cleanup(srv.Close)
	root := t.TempDir()
	m, err := plugins.NewManager(plugins.Config{
		CodeDir: filepath.Join(root, "plugins"), DataDir: filepath.Join(root, "data"),
		RegistryPath: filepath.Join(root, "plugins.json"), ArchiveBase: srv.URL, HTTPClient: srv.Client(), PanelVersion: "3.10.2",
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func menuOf(t *testing.T, h *pluginsHarness) plugins.UIState {
	t.Helper()
	r := h.call(t, http.MethodGet, "/plugins/menu", nil)
	if !r.Success {
		t.Fatalf("menu: %s", r.Msg)
	}
	var st plugins.UIState
	if err := json.Unmarshal(r.Obj, &st); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestInterfacePluginPagesAndMenuFollowItsLifecycle(t *testing.T) {
	m := newUIManager(t)
	h := newPluginsHarness(t, func() (*plugins.Manager, error) { return m, nil })

	p := h.call(t, http.MethodPost, "/plugins/preview", map[string]string{"url": "https://github.com/acme/page"})
	var pv plugins.Preview
	if err := json.Unmarshal(p.Obj, &pv); err != nil || !p.Success {
		t.Fatalf("preview: %s %v", p.Msg, err)
	}
	if r := h.call(t, http.MethodPost, "/plugins/install", map[string]any{"token": pv.Token, "permissions": pv.Manifest.Permissions}); !r.Success {
		t.Fatalf("install: %s", r.Msg)
	}

	if st := menuOf(t, h); len(st.Menu) != 0 || len(st.Replaces) != 0 {
		t.Fatalf("an installed but disabled plugin already shows in the interface: %+v", st)
	}
	if rec := rawCall(h, http.MethodGet, "/plugins/x/page-a/ui/index.html", ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("page of a disabled plugin = %d, want 503", rec.Code)
	}

	if r := h.call(t, http.MethodPost, "/plugins/enable/page-a", nil); !r.Success {
		t.Fatal(r.Msg)
	}
	st := menuOf(t, h)
	if len(st.Menu) != 1 || st.Menu[0].PluginID != "page-a" || st.Menu[0].Path != "/ui/index.html" || st.Menu[0].Icon != "bell" {
		t.Errorf("menu = %+v", st.Menu)
	}
	if got := st.Replaces["/clients"]; got.PluginID != "page-a" || got.Path != "/ui/replace/clients/" {
		t.Errorf("replaces = %+v", st.Replaces)
	}

	rec := rawCall(h, http.MethodGet, "/plugins/x/page-a/ui/index.html", "")
	if rec.Code != http.StatusOK || rec.Body.String() != "<h1>page a</h1>" {
		t.Fatalf("page = %d %q", rec.Code, rec.Body.String())
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox allow-scripts") || !strings.Contains(csp, "frame-ancestors 'self'") {
		t.Errorf("CSP = %q", csp)
	}
	if rec := rawCall(h, http.MethodGet, "/plugins/x/page-a/ui/replace/clients/", ""); rec.Code != http.StatusOK || rec.Body.String() != "<h1>clients</h1>" {
		t.Errorf("replacement page = %d %q", rec.Code, rec.Body.String())
	}
	if rec := rawCall(h, http.MethodGet, "/plugins/x/page-a/notes.txt", ""); rec.Code != http.StatusNotFound {
		t.Errorf("a file outside ui/ = %d, want 404", rec.Code)
	}
	if rec := rawCall(h, http.MethodPost, "/plugins/x/page-a/ui/index.html", "{}"); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST to a static page = %d, want 405", rec.Code)
	}

	if r := h.call(t, http.MethodPost, "/plugins/disable/page-a", nil); !r.Success {
		t.Fatal(r.Msg)
	}
	if st := menuOf(t, h); len(st.Menu) != 0 || len(st.Replaces) != 0 {
		t.Errorf("disabling must take the plugin out of the interface at once: %+v", st)
	}
	if r := h.call(t, http.MethodPost, "/plugins/enable/page-a", nil); !r.Success {
		t.Fatal(r.Msg)
	}
	if r := h.call(t, http.MethodPost, "/plugins/uninstall/page-a", map[string]bool{"removeData": true}); !r.Success {
		t.Fatal(r.Msg)
	}
	if st := menuOf(t, h); len(st.Menu) != 0 || len(st.Replaces) != 0 {
		t.Errorf("uninstall left traces in the interface: %+v", st)
	}
	if rec := rawCall(h, http.MethodGet, "/plugins/x/page-a/ui/index.html", ""); rec.Code != http.StatusNotFound {
		t.Errorf("page of an uninstalled plugin = %d, want 404", rec.Code)
	}
}

func TestMenuIsEmptyWhenPluginsAreOff(t *testing.T) {
	t.Setenv("XUI_PLUGINS_DISABLED", "1")
	h := newPluginsHarness(t, func() (*plugins.Manager, error) { return nil, plugins.ErrDisabled })
	if st := menuOf(t, h); len(st.Menu) != 0 || len(st.Replaces) != 0 {
		t.Errorf("safe mode menu = %+v", st)
	}
}
