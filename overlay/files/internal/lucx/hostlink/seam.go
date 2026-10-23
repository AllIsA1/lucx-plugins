// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostlink

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

const (
	// MaxSeamRequestBody is the largest request body handed to seams; a larger request runs on the panel alone.
	MaxSeamRequestBody = 1 << 20
	// MaxSeamResponseBody is the largest response body handed to the after seams.
	MaxSeamResponseBody = 8 << 20

	defaultSeamTimeout = 1500 * time.Millisecond
	seamCapability     = "seams"
	seamVersion        = 1
	maxSeamAnswer      = 3*MaxSeamResponseBody + 1<<20
)

// SeamRequest is an HTTP request as the plugin system sees it. Route is the gin pattern the panel
// resolved, relative to the engine ("api": to /panel/api).
type SeamRequest struct {
	Engine  string              `json:"engine"`
	Method  string              `json:"method"`
	Route   string              `json:"route"`
	Path    string              `json:"path"`
	Query   string              `json:"query"`
	Params  map[string]string   `json:"params,omitempty"`
	Headers map[string][]string `json:"headers,omitempty"`
	Body    []byte              `json:"body,omitempty"`
	User    string              `json:"user,omitempty"`
}

// SeamResponse is an HTTP response.
type SeamResponse struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers,omitempty"`
	Body    []byte              `json:"body,omitempty"`
}

// SeamPreResult: answer with Response when Responded, else carry on with Request (possibly changed).
type SeamPreResult struct {
	Responded bool         `json:"responded"`
	Request   SeamRequest  `json:"request"`
	Response  SeamResponse `json:"response"`
	Failures  int          `json:"failures"`
}

// SeamPostResult is the response to send; Changed says a plugin altered it.
type SeamPostResult struct {
	Response SeamResponse `json:"response"`
	Changed  bool         `json:"changed"`
	Failures int          `json:"failures"`
}

// seamRow is one line of the host's table of where seams exist.
type seamRow struct {
	Engine  string   `json:"engine"`
	Route   string   `json:"route"`
	Methods []string `json:"methods"`
	Pre     bool     `json:"pre"`
	Post    bool     `json:"post"`
}

// Protected routes: plugins can never take part in these, whatever they declare. The plugin system
// keeps the same list and refuses such seams at install; this copy makes the panel safe on its own,
// and a test compares both with testdata/seam_match.golden.
var (
	seamProtectedGroups = []string{"setting", "plugins", "lucx"}
	seamProtectedRoutes = []string{
		"/server/importDB", "/server/getDb", "/server/updatePanel", "/server/getMigration",
		"/nodes/mtls/*", "/nodes/updatePanel",
		"/backuptotgbot",
	}
)

func seamGroup(route string) string {
	group, _, _ := strings.Cut(strings.TrimPrefix(route, "/"), "/")
	return group
}

// IsProtectedSeamRoute reports whether plugins may never take part in the route.
func IsProtectedSeamRoute(engine, route string) bool {
	if engine != "api" {
		return false
	}
	if slices.Contains(seamProtectedGroups, seamGroup(route)) {
		return true
	}
	for _, p := range seamProtectedRoutes {
		if routeCovers(p, route) {
			return true
		}
	}
	return false
}

func routeCovers(pattern, route string) bool {
	if prefix, ok := strings.CutSuffix(pattern, "*"); ok && strings.HasSuffix(prefix, "/") {
		return strings.HasPrefix(route, prefix) && len(route) > len(prefix)
	}
	return pattern == route
}

// seamTakesPart is the rule shared with the plugin system: a seam declared on seamRoute for methods
// (none: all) takes part in a request with the given method that the panel resolved to route.
func seamTakesPart(engine, seamRoute string, methods []string, method, route string) bool {
	if IsProtectedSeamRoute(engine, route) {
		return false
	}
	if seamRoute != "*" && !routeCovers(seamRoute, route) {
		return false
	}
	if len(methods) == 0 {
		return true
	}
	if method == "HEAD" && !slices.Contains(methods, "HEAD") {
		method = "GET"
	}
	return slices.Contains(methods, method)
}

// SeamWanted tells whether some plugin listens at the route: pre is the call before the panel's own
// handler, post the call on its answer. Without a host that has seams both are false, so a request
// costs nothing but a table lookup.
func (l *Link) SeamWanted(engine, method, route string) (pre, post bool) {
	for _, row := range l.subs.Load().seams {
		if row.Engine != engine || !seamTakesPart(engine, row.Route, row.Methods, method, route) {
			continue
		}
		pre = pre || row.Pre
		post = post || row.Post
	}
	return pre, post
}

// SeamPre asks the host to run the before and replace seams. An error means "carry on as if there
// were no plugin".
func (l *Link) SeamPre(ctx context.Context, req SeamRequest) (SeamPreResult, error) {
	if len(req.Body) > MaxSeamRequestBody {
		return SeamPreResult{}, errors.New("the request body is too large for seams")
	}
	var out SeamPreResult
	if err := l.postSeam(ctx, "/internal/seam/pre", req, &out); err != nil {
		return SeamPreResult{}, err
	}
	return out, nil
}

// SeamPost asks the host to run the after seams on the panel's answer.
func (l *Link) SeamPost(ctx context.Context, req SeamRequest, resp SeamResponse) (SeamPostResult, error) {
	if len(resp.Body) > MaxSeamResponseBody || len(req.Body) > MaxSeamRequestBody {
		return SeamPostResult{}, errors.New("the body is too large for seams")
	}
	var out SeamPostResult
	in := struct {
		Request  SeamRequest  `json:"request"`
		Response SeamResponse `json:"response"`
	}{req, resp}
	if err := l.postSeam(ctx, "/internal/seam/post", in, &out); err != nil {
		return SeamPostResult{}, err
	}
	return out, nil
}

func (l *Link) postSeam(ctx context.Context, path string, in, out any) error {
	timeout := l.seamTimeout
	if timeout <= 0 {
		timeout = defaultSeamTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://host"+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := l.call.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %d", path, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxSeamAnswer+1))
	if err != nil {
		return err
	}
	if len(raw) > maxSeamAnswer {
		return errors.New("the answer is too large")
	}
	return json.Unmarshal(raw, out)
}
