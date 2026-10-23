// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostlink

import (
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/web/middleware"
)

type seen struct {
	mu   sync.Mutex
	reqs []*http.Request
}

func (s *seen) record(r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs = append(s.reqs, r)
}

func (s *seen) last(t *testing.T) *http.Request {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.reqs) == 0 {
		t.Fatal("the host received no request")
	}
	return s.reqs[len(s.reqs)-1]
}

func (s *seen) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.reqs)
}

func recordingHost(t *testing.T, s *seen, reply func(http.ResponseWriter, *http.Request)) string {
	return fakeHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		reply(w, r)
	}))
}

func okJSON(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"success":true,"msg":"","obj":{"ok":true}}`))
}

func TestAdminRequestReachesTheHostUnderItsOwnPathWithoutTheBasePath(t *testing.T) {
	var s seen
	l := newLink(t, recordingHost(t, &s, okJSON))
	h := panelEngine(l, "/secret", "root-admin", "")

	w := do(h, http.MethodGet, "/secret/panel/api/plugins/logs/hello?lines=5")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("response = %d %s, want the host's answer", w.Code, w.Body)
	}
	got := s.last(t)
	if got.URL.Path != "/panel/api/plugins/logs/hello" || got.URL.RawQuery != "lines=5" {
		t.Errorf("host saw %s?%s, want /panel/api/plugins/logs/hello?lines=5", got.URL.Path, got.URL.RawQuery)
	}
	if got.Header.Get("X-LucX-User") != "root-admin" {
		t.Errorf("X-LucX-User = %q, want the logged-in user", got.Header.Get("X-LucX-User"))
	}
	if got.Header.Get("X-LucX-Integration") != "1" {
		t.Errorf("X-LucX-Integration = %q, want the contract version", got.Header.Get("X-LucX-Integration"))
	}
}

func TestNodeSyncScopeIsPassedOnSoTheHostCanGateIt(t *testing.T) {
	var s seen
	l := newLink(t, recordingHost(t, &s, okJSON))
	do(panelEngine(l, "", "", "node-sync"), http.MethodGet, "/panel/api/plugins/list")
	if got := s.last(t).Header.Get("X-LucX-Scope"); got != "node-sync" {
		t.Errorf("X-LucX-Scope = %q, want node-sync", got)
	}
	if got := s.last(t).Header.Get("X-LucX-User"); got != "api-token" {
		t.Errorf("X-LucX-User = %q, want api-token for a call without a login", got)
	}
}

func TestWhatTheBrowserSentCannotPoseAsThePanelsVerdictOrLeakTheSession(t *testing.T) {
	var s seen
	l := newLink(t, recordingHost(t, &s, okJSON))
	h := panelEngine(l, "", "real-user", "")
	do(h, http.MethodPost, "/panel/api/plugins/enable/hello", func(r *http.Request) {
		r.Header.Set("X-LucX-User", "forged")
		r.Header.Set("X-LucX-Scope", "forged-scope")
		r.Header.Set("X-LucX-Allowed-Routes", "GET /everything")
		r.Header.Set("x-lucx-plugin-id", "forged-plugin")
		r.Header.Set("Cookie", "3x-ui=session-secret")
		r.Header.Set("Authorization", "Bearer panel-api-token")
		r.Header.Set("X-Csrf-Token", "csrf-secret")
	})
	got := s.last(t)
	if got.Header.Get("X-LucX-User") != "real-user" || got.Header.Get("X-LucX-Scope") != "" {
		t.Errorf("identity headers = %q / %q, want the panel's own values only", got.Header.Get("X-LucX-User"), got.Header.Get("X-LucX-Scope"))
	}
	for _, h := range []string{"X-LucX-Allowed-Routes", "X-LucX-Plugin-Id", "Cookie", "Authorization", "X-Csrf-Token"} {
		if v := got.Header.Get(h); v != "" {
			t.Errorf("header %s reached the host with %q", h, v)
		}
	}
}

func TestTheHostCannotSetCookiesOnThePanelsOrigin(t *testing.T) {
	l := newLink(t, fakeHost(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "3x-ui", Value: "hijack"})
		w.WriteHeader(200)
	})))
	w := do(panelEngine(l, "", "u", ""), http.MethodGet, "/panel/plugin-frame/tok/ui/index.html")
	if len(w.Result().Cookies()) != 0 {
		t.Errorf("response carried cookies %v, want none", w.Result().Cookies())
	}
}

func TestPluginPagesAndEscapedPathsGoThroughUntouched(t *testing.T) {
	var s seen
	l := newLink(t, recordingHost(t, &s, okJSON))
	h := panelEngine(l, "/b", "u", "")

	do(h, http.MethodGet, "/b/panel/plugin-frame/lxf.token/ui/a%20b/index.html")
	if p := s.last(t).URL.Path; p != "/panel/plugin-frame/lxf.token/ui/a b/index.html" {
		t.Errorf("frame path = %q", p)
	}
	do(h, http.MethodPost, "/b/panel/api/plugins/x/hello/api/step%2Fone")
	if p := s.last(t).URL.EscapedPath(); p != "/panel/api/plugins/x/hello/api/step%2Fone" {
		t.Errorf("escaped plugin path = %q, want the %%2F kept", p)
	}
	do(h, http.MethodHead, "/b/panel/plugin-frame/lxf.token/ui/index.html")
	if s.last(t).Method != http.MethodHead {
		t.Errorf("method = %s, want HEAD", s.last(t).Method)
	}
}

func TestBodyAndMethodSurviveTheHop(t *testing.T) {
	var gotBody, gotType string
	l := newLink(t, fakeHost(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 64)
		n, _ := r.Body.Read(b)
		gotBody, gotType = string(b[:n]), r.Header.Get("Content-Type")
		okJSON(w, r)
	})))
	h := panelEngine(l, "", "u", "")
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := srv.Client().Post(srv.URL+"/panel/api/plugins/install", "application/json", strings.NewReader(`{"url":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if gotBody != `{"url":"x"}` || gotType != "application/json" {
		t.Errorf("host got body %q type %q", gotBody, gotType)
	}
}

func TestWithoutAHostThePanelLooksAsItDidWithoutPlugins(t *testing.T) {
	l := newLink(t, socketPath(t, "nobody-listens.sock"))
	h := panelEngine(l, "", "u", "")

	menu := do(h, http.MethodGet, "/panel/api/plugins/menu")
	if menu.Code != 200 || !strings.Contains(menu.Body.String(), `"success":true`) || !strings.Contains(menu.Body.String(), `"menu":[]`) {
		t.Errorf("menu without a host = %d %s, want success with an empty menu", menu.Code, menu.Body)
	}
	list := do(h, http.MethodGet, "/panel/api/plugins/list")
	if list.Code != 200 || !strings.Contains(list.Body.String(), `"available":false`) || !strings.Contains(list.Body.String(), `"plugins":[]`) ||
		!strings.Contains(list.Body.String(), "not running") {
		t.Errorf("list without a host = %d %s, want available:false with the reason", list.Code, list.Body)
	}
	install := do(h, http.MethodPost, "/panel/api/plugins/install")
	if !strings.Contains(install.Body.String(), `"success":false`) || !strings.Contains(install.Body.String(), "not running") {
		t.Errorf("install without a host = %s, want a failure that says why", install.Body)
	}
	frame := do(h, http.MethodGet, "/panel/plugin-frame/tok/ui/index.html")
	if frame.Code != http.StatusServiceUnavailable {
		t.Errorf("a page without a host = %d, want 503", frame.Code)
	}
}

func TestSwitchedOffTheRoutesSayWhy(t *testing.T) {
	l := New(Config{HostSocket: socketPath(t, "x.sock"), Disabled: true})
	l.Start()
	defer l.Stop()
	w := do(panelEngine(l, "", "u", ""), http.MethodGet, "/panel/api/plugins/list")
	if !strings.Contains(w.Body.String(), "XUI_PLUGINS_DISABLED=1") {
		t.Errorf("list = %s, want the switch named", w.Body)
	}
}

func TestEveryMutationAsksForAFreshHandshake(t *testing.T) {
	var s seen
	l := newLink(t, recordingHost(t, &s, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/internal/hello":
			_, _ = w.Write([]byte(`{"integrationApi":1,"host":"t"}`))
		case "/internal/subscriptions":
			_, _ = w.Write([]byte(`{"events":["client.created"],"pipelines":[]}`))
		default:
			okJSON(w, r)
		}
	}))
	l.Start()
	waitFor(t, func() bool { return l.Status().Connected })
	before := s.count()
	do(panelEngine(l, "", "u", ""), http.MethodPost, "/panel/api/plugins/enable/hello")
	waitFor(t, func() bool { return s.count() >= before+3 }) // the call itself, then hello and subscriptions again
}

func TestOnlyReadsLeaveTheHandshakeAlone(t *testing.T) {
	var s seen
	l := newLink(t, recordingHost(t, &s, okJSON))
	do(panelEngine(l, "", "u", ""), http.MethodGet, "/panel/api/plugins/list")
	select {
	case <-l.kick:
		t.Error("a GET asked for a handshake")
	default:
	}
}

func TestTheRawWriterKeyIsTheMiddlewaresOne(t *testing.T) {
	if rawWriterKey != middleware.RawWriterKey {
		t.Errorf("rawWriterKey = %q, middleware uses %q: the write deadline would never be lifted", rawWriterKey, middleware.RawWriterKey)
	}
}

func TestRegisteredRoutesAreTheHostsRoutes(t *testing.T) {
	want, err := os.ReadFile("testdata/routes.golden")
	if err != nil {
		t.Fatal(err)
	}
	e := panelEngine(newLink(t, "x"), "/base", "", "")
	var got []string
	for _, r := range e.Routes() {
		got = append(got, r.Method+" "+strings.TrimPrefix(r.Path, "/base"))
	}
	sort.Strings(got)
	if strings.Join(got, "\n")+"\n" != string(want) {
		t.Errorf("registered routes differ from testdata/routes.golden (the host's table):\n%s", strings.Join(got, "\n"))
	}
}
