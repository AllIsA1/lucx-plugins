// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// The host server cuts every response at WriteTimeout; plugin calls that wait on a download or a
// process must lift that for themselves, or the browser sees a reset and retries the POST.
func TestLongPluginRequestsOutliveTheServerWriteTimeout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(rawWriterMiddleware())
	engine.GET("/cut", func(c *gin.Context) {
		time.Sleep(400 * time.Millisecond)
		c.String(http.StatusOK, "done")
	})
	engine.GET("/lifted", func(c *gin.Context) {
		extendWriteDeadline(c, 5*time.Second)
		time.Sleep(400 * time.Millisecond)
		c.String(http.StatusOK, "done")
	})
	srv := httptest.NewUnstartedServer(engine)
	srv.Config.WriteTimeout = 150 * time.Millisecond
	srv.Start()
	defer srv.Close()
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 5 * time.Second}

	get := func(path string) (string, error) {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+path, nil)
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		return string(raw), err
	}
	if body, err := get("/cut"); err == nil && body == "done" {
		t.Fatal("the control request survived the write timeout, so this test proves nothing")
	}
	if body, err := get("/lifted"); err != nil || body != "done" {
		t.Fatalf("a request that lifted the deadline = %q, %v", body, err)
	}
}
