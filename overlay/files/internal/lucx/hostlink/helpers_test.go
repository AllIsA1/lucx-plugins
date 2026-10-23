// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostlink

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/session"
)

func init() { gin.SetMode(gin.TestMode) }

// socketPath is short enough for a unix socket (t.TempDir paths can exceed the 108-byte limit).
func socketPath(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "hl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, name)
}

// fakeHost serves handler on a unix socket, the way the plugin host does.
func fakeHost(t *testing.T, handler http.Handler) string {
	t.Helper()
	sock := socketPath(t, "host.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return sock
}

// newLink is a link to socket that nobody else shares.
func newLink(t *testing.T, socket string) *Link {
	t.Helper()
	l := New(Config{HostSocket: socket, InternalSocket: socketPath(t, "panel.sock")})
	t.Cleanup(l.Stop)
	return l
}

// panelEngine is a stand-in for the panel: the plugin routes sit under base, behind a middleware
// that plays the login check by naming the user and scope.
func panelEngine(l *Link, base, user, scope string) *gin.Engine {
	e := gin.New()
	e.Use(sessions.Sessions("t", cookie.NewStore([]byte("secret"))))
	e.Use(func(c *gin.Context) {
		if user != "" {
			session.SetAPIAuthUser(c, &model.User{Username: user})
		}
		if scope != "" {
			c.Set("api_token_scope", scope)
		}
		c.Next()
	})
	root := e.Group(base)
	l.RegisterFrame(root)
	l.RegisterAdmin(root.Group("/panel/api/plugins"))
	return e
}

// result is a response read in full.
type result struct {
	Code int
	Body *bytes.Buffer
	resp *http.Response
}

func (r result) Result() *http.Response { return r.resp }

// do serves h on a real listener: gin's writer asks the response writer for CloseNotify, which a
// recorder cannot answer.
func do(h http.Handler, method, target string, mutate ...func(*http.Request)) result {
	srv := httptest.NewServer(h)
	defer srv.Close()
	req, err := http.NewRequest(method, srv.URL+target, nil)
	if err != nil {
		panic(err)
	}
	for _, m := range mutate {
		m(req)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	var body bytes.Buffer
	_, _ = body.ReadFrom(resp.Body)
	return result{Code: resp.StatusCode, Body: &body, resp: resp}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
