// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

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
