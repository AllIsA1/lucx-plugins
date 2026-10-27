// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package controller

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/lucx/hostlink"
	"github.com/mhsanaei/3x-ui/v3/internal/web/middleware"
	"github.com/mhsanaei/3x-ui/v3/internal/web/runtime"
)

const relayRouteGrant = "POST /lucx/plugin-relay/:nodeId"

type fakeNode struct {
	srv  *httptest.Server
	mu   sync.Mutex
	seen []string
	// reply decides the answer.
	reply func(w http.ResponseWriter, r *http.Request)
}

func (n *fakeNode) calls() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.seen...)
}

// relayHarness is the production auth wiring with the relay route, a node stored in the panel's
// database that points at a fake node panel, and both listeners' views of the engine.
func relayHarness(t *testing.T) (public http.Handler, internal http.Handler, node *fakeNode, nodeID int) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dbDir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbDir)
	dbtest.InitDB(t, filepath.Join(dbDir, "x-ui.db"))

	node = &fakeNode{reply: func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"msg":"","obj":{"plugins":[]}}`))
	}}
	node.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		node.mu.Lock()
		node.seen = append(node.seen, r.Method+" "+r.URL.RequestURI())
		node.mu.Unlock()
		node.reply(w, r)
	}))
	t.Cleanup(node.srv.Close)
	u, _ := url.Parse(node.srv.URL)
	port, _ := strconv.Atoi(u.Port())
	row := &model.Node{Name: "n1", Scheme: "http", Address: u.Hostname(), Port: port, BasePath: "/", ApiToken: "tok", Enable: true, AllowPrivateAddress: true, TlsVerifyMode: "verify"}
	if err := database.GetDB().Create(row).Error; err != nil {
		t.Fatal(err)
	}
	runtime.SetManager(runtime.NewManager(runtime.LocalDeps{}))

	engine := gin.New()
	engine.Use(sessions.Sessions("3x-ui", cookie.NewStore([]byte("relay-test-secret"))))
	a := &APIController{}
	api := engine.Group("/panel/api")
	api.Use(a.checkAPIAuth)
	api.Use(a.enforceTokenScope)
	api.Use(middleware.CSRFMiddleware())
	NewPluginRelayController(api.Group("/lucx"))
	// What a logged-in administrator's browser looks like on the public listener.
	adminEngine := gin.New()
	adminEngine.Use(func(c *gin.Context) { c.Set("api_authed", true); c.Next() })
	NewPluginRelayController(adminEngine.Group("/panel/api/lucx"))
	return adminEngine, hostlink.InternalHandler(engine, func() string { return "" }), node, row.Id
}

func relayCall(h http.Handler, nodeID int, grant string, req any) *httptest.ResponseRecorder {
	body, _ := json.Marshal(req)
	r := httptest.NewRequest(http.MethodPost, "/panel/api/lucx/plugin-relay/"+strconv.Itoa(nodeID), bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-LucX-Allowed-Routes", grant)
	r.Header.Set("X-LucX-Plugin-Id", "plugin-host")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func relayReq(method, path string, body any) map[string]any {
	m := map[string]any{"method": method, "path": path}
	if body != nil {
		m["body"] = body
	}
	return m
}

func TestRelayCarriesAllowedCallsToTheNodeAndMarksTheAnswer(t *testing.T) {
	_, internal, node, id := relayHarness(t)
	for _, c := range []struct{ method, path string }{
		{"GET", "/panel/api/plugins/list"},
		{"GET", "/panel/api/plugins/logs/hello?lines=50"},
		{"POST", "/panel/api/plugins/remote/install"},
		{"POST", "/panel/api/plugins/remote/update"},
		{"GET", "/panel/api/plugins/system"},
		{"POST", "/panel/api/plugins/system/check"},
		{"POST", "/panel/api/plugins/system/update"},
		{"POST", "/panel/api/plugins/enable/hello"},
		{"POST", "/panel/api/plugins/disable/hello"},
		{"POST", "/panel/api/plugins/restart/hello"},
		{"POST", "/panel/api/plugins/uninstall/hello"},
	} {
		w := relayCall(internal, id, relayRouteGrant, relayReq(c.method, c.path, map[string]any{"x": 1}))
		if w.Code != http.StatusOK || w.Header().Get("X-LucX-Relayed") != "node" || !strings.Contains(w.Body.String(), `"success":true`) {
			t.Errorf("%s %s = %d %s (relayed=%q)", c.method, c.path, w.Code, w.Body, w.Header().Get("X-LucX-Relayed"))
		}
	}
	if got := node.calls(); len(got) != 11 || got[1] != "GET /panel/api/plugins/logs/hello?lines=50" {
		t.Errorf("node saw %v", got)
	}
}

func TestRelayRefusesEverythingOutsideTheMastersList(t *testing.T) {
	_, internal, node, id := relayHarness(t)
	for name, req := range map[string]map[string]any{
		"an admin-only plugin route":       relayReq("POST", "/panel/api/plugins/install", nil),
		"the settings":                     relayReq("POST", "/panel/api/plugins/settings", nil),
		"a plugin's own API":               relayReq("POST", "/panel/api/plugins/x/hello/api/step", nil),
		"another part of the panel":        relayReq("POST", "/panel/api/server/updatePanel", nil),
		"the node's database":              relayReq("GET", "/panel/api/server/getDb", nil),
		"the wrong verb":                   relayReq("GET", "/panel/api/plugins/enable/hello", nil),
		"a list with a body verb":          relayReq("POST", "/panel/api/plugins/list", nil),
		"path climbing":                    relayReq("POST", "/panel/api/plugins/enable/hello/../../server/stopXrayService", nil),
		"an encoded slash":                 relayReq("POST", "/panel/api/plugins/enable/a%2Fb", nil),
		"an id that is not a plugin id":    relayReq("POST", "/panel/api/plugins/enable/Bad_ID", nil),
		"an extra segment":                 relayReq("POST", "/panel/api/plugins/enable/hello/extra", nil),
		"another host":                     relayReq("GET", "http://evil.example/panel/api/plugins/list", nil),
		"an encoded plugin id":             relayReq("POST", "/panel/api/plugins/enable/%68ello", nil),
		"log lines on another route":       relayReq("GET", "/panel/api/plugins/list?lines=5", nil),
		"a query on a route that has none": relayReq("GET", "/panel/api/plugins/list?x=1", nil),
		"an unknown query on the logs":     relayReq("GET", "/panel/api/plugins/logs/hello?cmd=1", nil),
		"a huge line count":                relayReq("GET", "/panel/api/plugins/logs/hello?lines=999999", nil),
		"a fragment":                       relayReq("GET", "/panel/api/plugins/list#x", nil),
		"no path":                          relayReq("GET", "", nil),
		"an unsupported verb":              relayReq("DELETE", "/panel/api/plugins/uninstall/hello", nil),
		"a path without the panel prefix":  relayReq("GET", "/plugins/list", nil),
		"a path with a doubled slash":      relayReq("GET", "/panel/api//plugins/list", nil),
		"a trailing slash":                 relayReq("GET", "/panel/api/plugins/list/", nil),
	} {
		t.Run(name, func(t *testing.T) {
			w := relayCall(internal, id, relayRouteGrant, req)
			if w.Code != http.StatusForbidden && w.Code != http.StatusBadRequest {
				t.Errorf("= %d %s, want it refused", w.Code, w.Body)
			}
			if w.Header().Get("X-LucX-Relayed") != "" {
				t.Error("a refusal of the panel itself was marked as a node's answer")
			}
		})
	}
	if got := node.calls(); len(got) != 0 {
		t.Errorf("the node was reached by refused calls: %v", got)
	}
}

func TestRelayIsReachableOnlyFromThePluginHostsSocket(t *testing.T) {
	public, internal, node, id := relayHarness(t)
	// An administrator's browser session is not enough: the route does not exist for it.
	if w := relayCall(public, id, relayRouteGrant, relayReq("GET", "/panel/api/plugins/list", nil)); w.Code != http.StatusNotFound {
		t.Errorf("relay for a browser = %d, want 404", w.Code)
	}
	// The host's socket needs the route in the list it names for itself.
	if w := relayCall(internal, id, "GET /nodes/list", relayReq("GET", "/panel/api/plugins/list", nil)); w.Code != http.StatusForbidden {
		t.Errorf("relay without the grant = %d, want 403", w.Code)
	}
	if got := node.calls(); len(got) != 0 {
		t.Errorf("the node was reached: %v", got)
	}
}

func TestRelayNeedsAnExistingEnabledNode(t *testing.T) {
	_, internal, node, id := relayHarness(t)
	if w := relayCall(internal, id+99, relayRouteGrant, relayReq("GET", "/panel/api/plugins/list", nil)); w.Code != http.StatusNotFound {
		t.Errorf("a node that does not exist = %d, want 404", w.Code)
	}
	if w := relayCall(internal, 0, relayRouteGrant, relayReq("GET", "/panel/api/plugins/list", nil)); w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound {
		t.Errorf("node 0 = %d, want it refused", w.Code)
	}
	if err := database.GetDB().Model(&model.Node{}).Where("id = ?", id).Update("enable", false).Error; err != nil {
		t.Fatal(err)
	}
	if w := relayCall(internal, id, relayRouteGrant, relayReq("GET", "/panel/api/plugins/list", nil)); w.Code != http.StatusConflict {
		t.Errorf("a disabled node = %d, want 409", w.Code)
	}
	if got := node.calls(); len(got) != 0 {
		t.Errorf("the node was reached: %v", got)
	}
}

func TestRelayPassesTheNodesOwnRefusalThroughWithItsStatus(t *testing.T) {
	_, internal, node, id := relayHarness(t)
	node.reply = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"success":false,"msg":"plugin management by the master is disabled on this node","obj":null}`)
	}
	w := relayCall(internal, id, relayRouteGrant, relayReq("GET", "/panel/api/plugins/list", nil))
	if w.Code != http.StatusForbidden || w.Header().Get("X-LucX-Relayed") != "node" || !strings.Contains(w.Body.String(), "disabled on this node") {
		t.Errorf("relay = %d %s (relayed=%q)", w.Code, w.Body, w.Header().Get("X-LucX-Relayed"))
	}
}

func TestRelayReportsAnUnreachableNodeAsABadGateway(t *testing.T) {
	_, internal, node, id := relayHarness(t)
	node.srv.Close()
	w := relayCall(internal, id, relayRouteGrant, relayReq("GET", "/panel/api/plugins/list", nil))
	if w.Code != http.StatusBadGateway || w.Header().Get("X-LucX-Relayed") != "" {
		t.Errorf("relay to a dead node = %d (relayed=%q), want 502 from the panel", w.Code, w.Header().Get("X-LucX-Relayed"))
	}
}

func TestRelayRefusesAMalformedRequest(t *testing.T) {
	_, internal, _, id := relayHarness(t)
	r := httptest.NewRequest(http.MethodPost, "/panel/api/lucx/plugin-relay/"+strconv.Itoa(id), strings.NewReader("not json"))
	r.Header.Set("X-LucX-Allowed-Routes", relayRouteGrant)
	w := httptest.NewRecorder()
	internal.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("malformed body = %d, want 400", w.Code)
	}
	r = httptest.NewRequest(http.MethodPost, "/panel/api/lucx/plugin-relay/abc", strings.NewReader(`{}`))
	r.Header.Set("X-LucX-Allowed-Routes", relayRouteGrant)
	w = httptest.NewRecorder()
	internal.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound {
		t.Errorf("a node id that is not a number = %d", w.Code)
	}
}

// The node enforces its own list of what a master may call; the relay must offer exactly that,
// no more (a call the node would refuse) and no less. The plugin system's repository compares the
// golden file with the node side.
func TestRelayRoutesAreTheMasterRoutesOfTheNode(t *testing.T) {
	want, err := os.ReadFile("testdata/master-routes.golden")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range relayRoutes {
		got = append(got, r.method+" "+r.pattern)
	}
	sort.Strings(got)
	if strings.Join(got, "\n")+"\n" != string(want) {
		t.Errorf("relayRoutes differ from testdata/master-routes.golden:\n%s", strings.Join(got, "\n"))
	}
}
