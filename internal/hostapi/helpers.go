// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostapi

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/AllIsA1/lucx-plugins/internal/logx"
)

// scopeNodeSync is the scope the panel reports for a call made by a master panel on behalf of its node-sync token.
const scopeNodeSync = "node-sync"

// Headers the panel integration sets after it has authenticated the caller; the host trusts them
// because it is reachable only on the panel's unix socket.
const (
	headerUser  = "X-LucX-User"
	headerScope = "X-LucX-Scope"
)

// Msg is the envelope every panel API answers with.
type Msg struct {
	Success bool   `json:"success"`
	Msg     string `json:"msg"`
	Obj     any    `json:"obj"`
}

func jsonMsg(c *gin.Context, msg string, err error) { jsonMsgObj(c, msg, nil, err) }

func jsonObj(c *gin.Context, obj any, err error) { jsonMsgObj(c, "", obj, err) }

func jsonMsgObj(c *gin.Context, msg string, obj any, err error) {
	m := Msg{Obj: obj}
	if err == nil {
		m.Success = true
		m.Msg = msg
	} else {
		errStr := err.Error()
		switch {
		case errStr != "":
			m.Msg = msg + " (" + errStr + ")"
			logx.Warningf("[%s %s] %s failed: %v", c.Request.Method, c.Request.URL.Path, msg, err)
		case msg != "":
			m.Msg = msg
			logx.Warningf("[%s %s] %s failed", c.Request.Method, c.Request.URL.Path, msg)
		default:
			m.Msg = "Something went wrong"
		}
	}
	c.JSON(http.StatusOK, m)
}

func pureJsonMsg(c *gin.Context, statusCode int, success bool, msg string) {
	c.JSON(statusCode, Msg{Success: success, Msg: msg})
}

// relAPIPath is the part of a route after /panel/api.
func relAPIPath(fullPath string) string {
	_, after, ok := strings.Cut(fullPath, "/panel/api")
	if !ok {
		return ""
	}
	return after
}

// callerContext copies the identity the panel integration vouches for into the gin context.
func callerContext() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("lucx_user", c.GetHeader(headerUser))
		c.Set("api_token_scope", c.GetHeader(headerScope))
		c.Next()
	}
}

// loginUser is the panel user behind the request ("api-token" for a token call).
func loginUser(c *gin.Context) string {
	if u := c.GetString("lucx_user"); u != "" {
		return u
	}
	return "api-token"
}

const rawWriterKey = "lucx_raw_writer"

// rawWriterMiddleware keeps the response writer so a handler can reach http.ResponseController.
func rawWriterMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(rawWriterKey, c.Writer)
		c.Next()
	}
}
