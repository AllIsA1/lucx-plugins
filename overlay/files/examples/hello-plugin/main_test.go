// Copyright (c) 2026 LucX-UI Project.
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakePanel(t *testing.T, wantToken, haveToken string, clients int) *Panel {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+wantToken || r.URL.Path != "/panel/api/clients/list" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		list := make([]int, clients)
		obj, _ := json.Marshal(list)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "obj": json.RawMessage(obj)})
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "panel-token"), []byte(haveToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LUCX_PANEL_URL", srv.URL)
	t.Setenv("CREDENTIALS_DIRECTORY", dir)
	p, err := NewPanel()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHelloEchoesThePanelUser(t *testing.T) {
	h := newMux(nil, t.TempDir())
	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	req.Header.Set("X-LucX-User", "admin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"user":"admin"`) {
		t.Fatalf("GET /hello = %d %s", rec.Code, rec.Body.String())
	}
}

func TestClientsCallsThePanelWithTheIssuedToken(t *testing.T) {
	h := newMux(fakePanel(t, "lxp.hello.sig", "lxp.hello.sig", 3), t.TempDir())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/clients", nil))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"clients":3}` {
		t.Fatalf("GET /clients = %d %s", rec.Code, rec.Body.String())
	}
}

func TestClientsAnswers503WithoutAPanelToken(t *testing.T) {
	rec := httptest.NewRecorder()
	newMux(nil, t.TempDir()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/clients", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestCounterPersistsAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	call := func() string {
		rec := httptest.NewRecorder()
		newMux(nil, dir).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/counter", nil))
		return strings.TrimSpace(rec.Body.String())
	}
	if got := call(); got != `{"counter":1}` {
		t.Fatalf("first = %s", got)
	}
	if got := call(); got != `{"counter":2}` {
		t.Fatalf("second (new mux, same state dir) = %s", got)
	}
}

func TestClientsAnswers502WhenThePanelRefusesTheToken(t *testing.T) {
	rec := httptest.NewRecorder()
	newMux(fakePanel(t, "lxp.hello.sig", "lxp.hello.forged", 3), t.TempDir()).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/clients", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 when the panel answers 403", rec.Code)
	}
}

func TestDeliveredEventsAreRecorded(t *testing.T) {
	h := newMux(nil, t.TempDir())
	post := func(body string) int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/_lucx/events", strings.NewReader(body)))
		return rec.Code
	}
	if code := post(`{"id":"e1","type":"client.created","source":"alice@example.com","time":"2026-10-18T12:00:00Z","data":{"email":"alice@example.com"}}`); code != http.StatusNoContent {
		t.Fatalf("event answered %d, want 204", code)
	}
	if code := post("{not json"); code != http.StatusBadRequest {
		t.Errorf("a malformed event answered %d, want 400", code)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/events", nil))
	var got []Event
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got) != 1 || got[0].Source != "alice@example.com" || got[0].Type != "client.created" {
		t.Fatalf("GET /events = %s (%v)", rec.Body.String(), err)
	}
}

func TestPagesComeFromTheBinary(t *testing.T) {
	h := newMux(nil, t.TempDir())
	for path, want := range map[string]string{
		"/ui/index.html": "text/html",
		"/ui/":           "text/html",
		"/ui/app.js":     "text/javascript",
		"/ui/style.css":  "text/css",
		"/ui/lucx-ui.js": "text/javascript",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), want) || rec.Body.Len() == 0 {
			t.Errorf("GET %s = %d %q (%d bytes), want %s", path, rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len(), want)
		}
	}
}

func TestPagesRefuseWhatIsNotAPage(t *testing.T) {
	h := newMux(nil, t.TempDir())
	for path, want := range map[string]int{
		"/ui/missing.html":  http.StatusNotFound,
		"/ui/../main.go":    http.StatusNotFound,
		"/ui/%2e%2e/go.mod": http.StatusNotFound,
		"/ui/sub/":          http.StatusNotFound,
		"/main.go":          http.StatusNotFound,
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want && !(rec.Code == http.StatusMovedPermanently || rec.Code == http.StatusTemporaryRedirect) {
			t.Errorf("GET %s = %d, want %d", path, rec.Code, want)
		}
		if strings.Contains(rec.Body.String(), "package main") {
			t.Errorf("GET %s leaked source", path)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/ui/index.html", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST a page = %d, want 405", rec.Code)
	}
}

// The panel's bridge only reaches /api/..., so the page's calls need that prefix.
func TestTheBackendAnswersBelowAPIToo(t *testing.T) {
	h := newMux(nil, t.TempDir())
	for _, c := range []struct{ method, path, want string }{
		{http.MethodGet, "/api/hello", `"message":"hello"`},
		{http.MethodPost, "/api/counter", `{"counter":1}`},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("%s %s = %d %s", c.method, c.path, rec.Code, rec.Body.String())
		}
	}
}

func TestInstallHookLeavesAMarkerInTheStateDirectory(t *testing.T) {
	dir := t.TempDir()
	rec := httptest.NewRecorder()
	newMux(nil, dir).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/_lucx/install", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, "installed")); err != nil {
		t.Errorf("the install hook left no marker: %v", err)
	}
}

func TestUninstallHookLeavesAMarkerInTheStateDirectory(t *testing.T) {
	dir := t.TempDir()
	rec := httptest.NewRecorder()
	newMux(nil, dir).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/_lucx/uninstall", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, "uninstalled")); err != nil {
		t.Errorf("the uninstall hook left no marker: %v", err)
	}
}
