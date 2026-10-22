// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostapi

import (
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
)

// Server is the host's HTTP surface on the unix socket the panel integration talks to.
type Server struct {
	svc    *PluginService
	engine *gin.Engine
	hooks  Hooks
}

// NewServer registers the panel's plugin routes under the same paths the frontend already calls.
func NewServer(svc *PluginService) *Server {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	engine.Use(gin.Recovery(), rawWriterMiddleware())
	NewPluginFrameController(&engine.RouterGroup, svc)
	NewPluginsController(engine.Group("/panel/api/plugins"), svc)
	s := &Server{svc: svc, engine: engine, hooks: DefaultHooks(svc, "dev")}
	s.registerInternal()
	return s
}

// Handler is the http.Handler to serve on the socket.
func (s *Server) Handler() http.Handler { return s.engine }

// Listen opens the unix socket, replacing a stale file, readable and writable by root only.
func Listen(path string) (net.Listener, error) {
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
	return ln, nil
}
