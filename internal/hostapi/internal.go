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
	// A request body a seam sees is at most 1 MiB; as base64 inside JSON it is a third larger.
	maxSeamPreBody  = 2 << 20
	maxSeamPostBody = 16 << 20
)

// Capabilities are the optional parts of the contract this host has, by name and version. A panel
// uses a part only when the host lists it, so adding one never needs a new integrationApi.
var Capabilities = map[string]int{"seams": 1}

// Hooks is what the internal routes call; they default to the running supervisor.
type Hooks struct {
	Version       string
	Dispatch      func(plugins.Event)
	Transform     func(ctx context.Context, name string, doc []byte, validate func(before, after []byte) error) []byte
	Subscriptions func() (events, pipelines []string)
	SeamTable     func() []plugins.SeamTableRow
	SeamPre       func(ctx context.Context, req plugins.SeamRequest) plugins.SeamPreResult
	SeamPost      func(ctx context.Context, req plugins.SeamRequest, resp plugins.SeamResponse) plugins.SeamPostResult
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
		SeamTable: func() []plugins.SeamTableRow {
			if sup := svc.supervisor(); sup != nil {
				return sup.SeamTable()
			}
			return nil
		},
		SeamPre: func(ctx context.Context, req plugins.SeamRequest) plugins.SeamPreResult {
			if sup := svc.supervisor(); sup != nil {
				return sup.SeamPre(ctx, req)
			}
			return plugins.SeamPreResult{Request: req}
		},
		SeamPost: func(ctx context.Context, req plugins.SeamRequest, resp plugins.SeamResponse) plugins.SeamPostResult {
			if sup := svc.supervisor(); sup != nil {
				return sup.SeamPost(ctx, req, resp)
			}
			return plugins.SeamPostResult{Response: resp}
		},
	}
}

// SetHooks replaces what the internal routes call.
func (s *Server) SetHooks(h Hooks) { s.hooks = h }

func (s *Server) registerInternal() {
	g := s.engine.Group("/internal")
	g.GET("/hello", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"integrationApi": IntegrationAPI, "host": s.hooks.Version, "capabilities": Capabilities})
	})
	g.GET("/subscriptions", func(c *gin.Context) {
		events, pipelines := s.hooks.Subscriptions()
		seams := []plugins.SeamTableRow{}
		if s.hooks.SeamTable != nil {
			if rows := s.hooks.SeamTable(); rows != nil {
				seams = rows
			}
		}
		c.JSON(http.StatusOK, gin.H{"events": nonNil(events), "pipelines": nonNil(pipelines), "seams": seams})
	})
	g.POST("/events", func(c *gin.Context) {
		var ev plugins.Event
		if !readJSON(c, maxEventBody, &ev) {
			return
		}
		s.hooks.Dispatch(ev)
		c.Status(http.StatusAccepted)
	})
	g.POST("/seam/pre", func(c *gin.Context) {
		var req plugins.SeamRequest
		if !readJSON(c, maxSeamPreBody, &req) {
			return
		}
		if !validSeamRequest(req) {
			c.Status(http.StatusBadRequest)
			return
		}
		if s.hooks.SeamPre == nil {
			c.JSON(http.StatusOK, plugins.SeamPreResult{Request: req})
			return
		}
		c.JSON(http.StatusOK, s.hooks.SeamPre(c.Request.Context(), req))
	})
	g.POST("/seam/post", func(c *gin.Context) {
		var in struct {
			Request  plugins.SeamRequest   `json:"request"`
			Response *plugins.SeamResponse `json:"response"`
		}
		if !readJSON(c, maxSeamPostBody, &in) {
			return
		}
		if !validSeamRequest(in.Request) || in.Response == nil {
			c.Status(http.StatusBadRequest)
			return
		}
		if s.hooks.SeamPost == nil {
			c.JSON(http.StatusOK, plugins.SeamPostResult{Response: *in.Response})
			return
		}
		c.JSON(http.StatusOK, s.hooks.SeamPost(c.Request.Context(), in.Request, *in.Response))
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

func validSeamRequest(r plugins.SeamRequest) bool {
	return (r.Engine == plugins.SeamEngineAPI || r.Engine == plugins.SeamEngineSub) && r.Method != "" && r.Route != ""
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
