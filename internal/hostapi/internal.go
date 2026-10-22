// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/AllIsA1/lucx-plugins/internal/plugins"
)

// IntegrationAPI is the version of the contract between the panel integration and the host.
const IntegrationAPI = 1

const (
	maxEventBody    = 1 << 20
	maxPipelineBody = 16 << 20
)

// Hooks is what the internal routes call; they default to the running supervisor.
type Hooks struct {
	Version       string
	Dispatch      func(plugins.Event)
	Transform     func(ctx context.Context, name string, doc []byte, validate func(before, after []byte) error) []byte
	Subscriptions func() (events, pipelines []string)
}

// DefaultHooks binds the hooks to the plugin supervisor of svc (none: events are dropped, documents pass).
func DefaultHooks(svc *PluginService, version string) Hooks {
	return Hooks{
		Version: version,
		Dispatch: func(ev plugins.Event) {
			if sup := svc.supervisor(); sup != nil {
				sup.Dispatch(ev)
			}
		},
		Transform: func(ctx context.Context, name string, doc []byte, validate func(before, after []byte) error) []byte {
			if sup := svc.supervisor(); sup != nil {
				out, _ := sup.Transform(ctx, name, doc, validate)
				return out
			}
			return doc
		},
		Subscriptions: func() ([]string, []string) {
			if sup := svc.supervisor(); sup != nil {
				return sup.Subscriptions()
			}
			return nil, nil
		},
	}
}

// SetHooks replaces what the internal routes call.
func (s *Server) SetHooks(h Hooks) { s.hooks = h }

func (s *Server) registerInternal() {
	g := s.engine.Group("/internal")
	g.GET("/hello", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"integrationApi": IntegrationAPI, "host": s.hooks.Version})
	})
	g.GET("/subscriptions", func(c *gin.Context) {
		events, pipelines := s.hooks.Subscriptions()
		c.JSON(http.StatusOK, gin.H{"events": nonNil(events), "pipelines": nonNil(pipelines)})
	})
	g.POST("/events", func(c *gin.Context) {
		var ev plugins.Event
		if !readJSON(c, maxEventBody, &ev) {
			return
		}
		s.hooks.Dispatch(ev)
		c.Status(http.StatusAccepted)
	})
	g.POST("/pipeline/:name", func(c *gin.Context) {
		name := c.Param("name")
		validate, ok := pipelineValidators[name]
		if !ok {
			c.Status(http.StatusNotFound)
			return
		}
		doc, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, maxPipelineBody))
		if err != nil {
			c.Status(http.StatusRequestEntityTooLarge)
			return
		}
		c.Data(http.StatusOK, "application/json", s.hooks.Transform(c.Request.Context(), name, doc, validate))
	})
}

// pipelineValidators are the rules a plugin's output must keep, per pipeline, checked between plugins.
var pipelineValidators = map[string]func(before, after []byte) error{
	plugins.PipelineXrayConfig: validateXrayPatch,
}

func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

func readJSON(c *gin.Context, limit int64, into any) bool {
	raw, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, limit))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			c.Status(http.StatusRequestEntityTooLarge)
		} else {
			c.Status(http.StatusBadRequest)
		}
		return false
	}
	if err := json.Unmarshal(raw, into); err != nil {
		c.Status(http.StatusBadRequest)
		return false
	}
	return true
}
