// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostlink

import (
	"context"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/session"
)

// miniPanel is a panel API with the same gate as the real one: a request from the host is decided
// by AuthorizeHostCall, anything else is refused as a stranger.
func miniPanel(base string, admin *model.User) (*gin.Engine, *[]string) {
	var served []string
	e := gin.New()
	api := e.Group(base + "/panel/api")
	api.Use(func(c *gin.Context) {
		if !FromHost(c) {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		if !AuthorizeHostCall(c, func() *model.User { return admin }) {
			return
		}
		c.Next()
	})
	ok := func(c *gin.Context) {
		u := session.GetLoginUser(c)
		who := ""
		if u != nil {
			who = u.Username
		}
		served = append(served, c.Request.Method+" "+c.FullPath()+" as "+who+" for "+c.GetString("plugin_id")+" authed="+boolStr(c.GetBool("api_authed")))
		c.JSON(200, gin.H{"success": true})
	}
	api.GET("/clients/list", ok)
	api.POST("/clients/list", ok)
	api.POST("/clients/del/:email", ok)
	api.POST("/server/updatePanel", ok)
	api.GET("/inbounds/list", ok)
	e.GET(base+"/login", func(c *gin.Context) { c.String(200, "login page") })
	return e, &served
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func hostCaller(allowed, plugin string) func(*http.Request) {
	return func(r *http.Request) {
		r.Header.Set("X-LucX-Allowed-Routes", allowed)
		r.Header.Set("X-LucX-Plugin-Id", plugin)
	}
}

func TestACallFromTheHostRunsAsTheAdminForExactlyTheNamedRoutes(t *testing.T) {
	e, served := miniPanel("", &model.User{Username: "boss"})
	h := InternalHandler(e, func() string { return "" })

	w := do(h, http.MethodGet, "/panel/api/clients/list", hostCaller("GET /clients/list,POST /clients/del/:email", "billing"))
	if w.Code != 200 {
		t.Fatalf("allowed route = %d %s", w.Code, w.Body)
	}
	if got := (*served)[0]; got != "GET /panel/api/clients/list as boss for billing authed=true" {
		t.Errorf("handler saw %q", got)
	}
	if w := do(h, http.MethodPost, "/panel/api/clients/del/a%40x.com", hostCaller("GET /clients/list,POST /clients/del/:email", "billing")); w.Code != 200 {
		t.Errorf("a parametrised allowed route = %d, want 200", w.Code)
	}
}

func TestARouteNotOnTheListIsRefusedBeforeItsHandlerRuns(t *testing.T) {
	e, served := miniPanel("", &model.User{Username: "boss"})
	h := InternalHandler(e, func() string { return "" })
	allowed := "GET /clients/list,POST /clients/del/:email"

	cases := map[string]struct {
		method, path, allowed string
	}{
		"another route":           {"GET", "/panel/api/inbounds/list", allowed},
		"the panel update":        {"POST", "/panel/api/server/updatePanel", allowed},
		"right path, wrong verb":  {"POST", "/panel/api/clients/list", allowed},
		"literal instead of :var": {"POST", "/panel/api/clients/del/a@x.com", "POST /clients/del/a@x.com"},
		"no list at all":          {"GET", "/panel/api/clients/list", ""},
		"junk list":               {"GET", "/panel/api/clients/list", "everything,;;,GET"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			w := do(h, c.method, c.path, hostCaller(c.allowed, "evil"))
			if w.Code != http.StatusForbidden {
				t.Errorf("%s %s with %q = %d, want 403", c.method, c.path, c.allowed, w.Code)
			}
		})
	}
	if len(*served) != 0 {
		t.Errorf("handlers ran for refused calls: %v", *served)
	}
}

func TestNothingBesideThePanelAPIIsReachableOnTheSocket(t *testing.T) {
	e, _ := miniPanel("", nil)
	h := InternalHandler(e, func() string { return "" })
	for _, p := range []string{"/login", "/panel/", "/panel/apix/clients/list", "/"} {
		if w := do(h, http.MethodGet, p, hostCaller("GET /clients/list", "p")); w.Code != http.StatusNotFound {
			t.Errorf("GET %s on the socket = %d, want 404", p, w.Code)
		}
	}
}

func TestTheBasePathIsAddedSoCallersNeedNotKnowIt(t *testing.T) {
	e, served := miniPanel("/hidden-base", &model.User{Username: "boss"})
	h := InternalHandler(e, func() string { return "/hidden-base/" })
	w := do(h, http.MethodGet, "/panel/api/clients/list", hostCaller("GET /clients/list", "p"))
	if w.Code != 200 || len(*served) != 1 {
		t.Fatalf("with a base path = %d %s", w.Code, w.Body)
	}
}

func TestThePublicListenerNeverTrustsTheHeadersOfAClient(t *testing.T) {
	e, served := miniPanel("", &model.User{Username: "boss"})
	// The engine itself, as the public listener serves it: no wrapper, so no mark.
	w := do(e, http.MethodGet, "/panel/api/clients/list", hostCaller("GET /clients/list", "forged"))
	if w.Code != http.StatusUnauthorized || len(*served) != 0 {
		t.Errorf("a client claiming to be the host = %d, served %v; want 401 and nothing served", w.Code, *served)
	}
}

func TestThePluginsOwnCredentialsAreNotPassedToHandlers(t *testing.T) {
	var auth, cookie string
	e := gin.New()
	e.GET("/panel/api/probe", func(c *gin.Context) {
		auth, cookie = c.GetHeader("Authorization"), c.GetHeader("Cookie")
		c.Status(200)
	})
	h := InternalHandler(e, func() string { return "" })
	do(h, http.MethodGet, "/panel/api/probe", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer lxp.something")
		r.Header.Set("Cookie", "3x-ui=x")
	})
	if auth != "" || cookie != "" {
		t.Errorf("handler saw Authorization %q Cookie %q", auth, cookie)
	}
}

func TestParseRoutesKeepsOnlyWellFormedEntries(t *testing.T) {
	got := parseRoutes(" GET /a , POST /b/:id,bogus,GET nopath,,PUT /c/*path ")
	want := []Route{{"GET", "/a"}, {"POST", "/b/:id"}, {"PUT", "/c/*path"}}
	if len(got) != len(want) {
		t.Fatalf("parsed %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestTheInternalSocketIsPrivateAndWorks(t *testing.T) {
	e, _ := miniPanel("", &model.User{Username: "boss"})
	sock := socketPath(t, "run/internal.sock")
	stop, err := ListenInternal(sock, InternalHandler(e, func() string { return "" }))
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(sock)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("socket mode = %v, want 0600", st.Mode().Perm())
	}
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}}
	req, _ := http.NewRequest(http.MethodGet, "http://panel/panel/api/clients/list", nil)
	hostCaller("GET /clients/list", "p")(req)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	stop()
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Errorf("socket file still there after stop: %v", err)
	}
	if strings.Contains(sock, "..") {
		t.Fatal("unreachable")
	}
}

func TestLinkServesTheInternalSocketUntilStopped(t *testing.T) {
	e, _ := miniPanel("/b", &model.User{Username: "boss"})
	l := New(Config{HostSocket: socketPath(t, "h.sock"), InternalSocket: socketPath(t, "run/panel.sock")})
	if err := l.ServeInternal(e, func() string { return "/b" }); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", l.cfg.InternalSocket)
	}}}
	req, _ := http.NewRequest(http.MethodGet, "http://panel/panel/api/clients/list", nil)
	hostCaller("GET /clients/list", "p")(req)
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("call through the socket = %v, %v", resp, err)
	}
	resp.Body.Close()
	l.Stop()
	if _, err := os.Stat(l.cfg.InternalSocket); !os.IsNotExist(err) {
		t.Errorf("socket still there after Stop: %v", err)
	}
}

func TestSwitchedOffThereIsNoInternalSocket(t *testing.T) {
	l := New(Config{HostSocket: socketPath(t, "h.sock"), InternalSocket: socketPath(t, "run/panel.sock"), Disabled: true})
	if err := l.ServeInternal(http.NotFoundHandler(), func() string { return "" }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(l.cfg.InternalSocket); !os.IsNotExist(err) {
		t.Errorf("a switched-off integration opened %s", l.cfg.InternalSocket)
	}
}
