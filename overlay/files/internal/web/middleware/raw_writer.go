// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package middleware

import "github.com/gin-gonic/gin"

// RawWriterKey holds the response writer as it was before any wrapping middleware (gzip) hid it.
const RawWriterKey = "lucx_raw_writer"

// RawWriterMiddleware keeps the unwrapped writer so a handler can reach http.ResponseController
// (per-request write deadlines); the gzip wrapper does not expose it. Register it before gzip.
func RawWriterMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(RawWriterKey, c.Writer)
		c.Next()
	}
}
