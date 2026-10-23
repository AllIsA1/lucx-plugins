// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostlink

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/network"
	"github.com/mhsanaei/3x-ui/v3/internal/web/session"
)

const (
	headerAllowed = "X-LucX-Allowed-Routes"
	headerPlugin  = "X-LucX-Plugin-Id"
	apiPrefix     = "/panel/api/"
)

// hostCall marks a request that arrived on the internal socket. It lives in the request's context,
// which only the socket's own wrapper can set: nothing a client sends on the public listener can
// carry it.
type hostCall struct {
	pluginID string
	allowed  []Route
}

type hostCallKey struct{}

// InternalHandler wraps the panel's engine for the internal socket: only /panel/api/ is reachable,
// the panel's base path is added so callers need not know it, and the request is marked as coming
// from the host together with the plugin it acts for and the routes that plugin may call.
func InternalHandler(engine http.Handler, basePath func() string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, apiPrefix) {
			http.NotFound(w, r)
			return
		}
		call := hostCall{pluginID: r.Header.Get(headerPlugin), allowed: parseRoutes(r.Header.Get(headerAllowed))}
		r = r.WithContext(context.WithValue(r.Context(), hostCallKey{}, call))
		base := strings.TrimSuffix(basePath(), "/")
		r2 := r.Clone(r.Context())
		r2.URL.Path = base + r.URL.Path
		if r.URL.RawPath != "" {
			r2.URL.RawPath = base + r.URL.RawPath
		}
		// The plugin's own headers never reach a handler as if the panel had set them.
		r2.Header.Del("Authorization")
		r2.Header.Del("Cookie")
		engine.ServeHTTP(w, r2)
	})
}

func parseRoutes(list string) []Route {
	var out []Route
	for _, part := range strings.Split(list, ",") {
		method, pattern, ok := strings.Cut(strings.TrimSpace(part), " ")
		if ok && method != "" && strings.HasPrefix(pattern, "/") {
			out = append(out, Route{Method: method, Path: pattern})
		}
	}
	return out
}

// FromHost reports whether c arrived on the internal socket.
func FromHost(c *gin.Context) bool {
	_, ok := c.Request.Context().Value(hostCallKey{}).(hostCall)
	return ok
}

// relAPIPath is the part of a route pattern after /panel/api.
func relAPIPath(fullPath string) string {
	_, after, ok := strings.Cut(fullPath, "/panel/api")
	if !ok {
		return ""
	}
	return after
}

// AuthorizeHostCall decides a request that came through the internal socket. The caller is the
// administrator, but only for the routes the host named: the route is the one gin resolved, so a
// crafted URL cannot reach something the list does not name. It returns false after answering.
func AuthorizeHostCall(c *gin.Context, admin func() *model.User) bool {
	call, _ := c.Request.Context().Value(hostCallKey{}).(hostCall)
	rel := relAPIPath(c.FullPath())
	allowed := false
	for _, r := range call.allowed {
		if r.Method == c.Request.Method && r.Path == rel {
			allowed = true
			break
		}
	}
	if !allowed {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"success": false, "msg": "this plugin may not call this route"})
		return false
	}
	if u := admin(); u != nil {
		session.SetAPIAuthUser(c, u)
	}
	// The same flag a bearer token sets: it lets a mutation through the CSRF check, which protects
	// browser sessions only.
	c.Set("api_authed", true)
	c.Set("plugin_id", call.pluginID)
	return true
}

// ListenInternal opens the internal socket (root only) and serves handler on it until the returned
// stop function is called.
func ListenInternal(path string, handler http.Handler) (stop func(), err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go network.ServeHTTP(srv, ln, "Plugin host socket")
	return func() {
		_ = srv.Close()
		_ = os.Remove(path)
	}, nil
}
