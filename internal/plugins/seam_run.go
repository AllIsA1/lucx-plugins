// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/AllIsA1/lucx-plugins/internal/logx"
)

const (
	defaultSeamTimeout          = 300 * time.Millisecond
	defaultSeamBreakerThreshold = 5
	defaultSeamBreakerCooldown  = 30 * time.Second

	maxSeamBody     = 8 << 20 // a response a plugin may produce
	maxSeamRequest  = 1 << 20 // a request body the panel hands over; larger ones skip the seams
	maxSeamHeaders  = 64
	maxSeamHeader   = 8 << 10
	maxSeamInflight = 32
	seamPath        = reservedPrefix + "/seam/"

	seamActionContinue = "continue"
	seamActionRespond  = "respond"
	seamActionFallback = "fallback"
	seamActionKeep     = "keep"
	seamActionModify   = "modify"
)

// SeamRequest is the HTTP request a seam sees. Method, route and path are the panel's and stay as
// they are; a plugin may change only the query, the body and the allowed headers.
type SeamRequest struct {
	Engine  string              `json:"engine"`
	Method  string              `json:"method"`
	Route   string              `json:"route"` // the gin pattern the panel resolved, relative to the engine
	Path    string              `json:"path"`
	Query   string              `json:"query"`
	Params  map[string]string   `json:"params,omitempty"`
	Headers map[string][]string `json:"headers,omitempty"`
	Body    []byte              `json:"body,omitempty"`
	User    string              `json:"user,omitempty"` // the logged-in panel user, empty on the public subscription server
}

// SeamResponse is an HTTP response a seam sees or produces.
type SeamResponse struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers,omitempty"`
	Body    []byte              `json:"body,omitempty"`
}

// SeamRequestDelta and SeamResponseDelta are what a plugin sends back: only what it changes.
type SeamRequestDelta struct {
	Query   *string             `json:"query,omitempty"`
	Headers map[string][]string `json:"headers,omitempty"` // merged; an empty list removes the header
	Body    *[]byte             `json:"body,omitempty"`
}

type SeamResponseDelta struct {
	Status  *int                `json:"status,omitempty"`
	Headers map[string][]string `json:"headers,omitempty"`
	Body    *[]byte             `json:"body,omitempty"`
}

type seamReply struct {
	Action   string             `json:"action"`
	Request  *SeamRequestDelta  `json:"request,omitempty"`
	Response *SeamResponseDelta `json:"response,omitempty"`
}

type seamInfo struct {
	Engine string `json:"engine"`
	Mode   string `json:"mode"`
	Route  string `json:"route"`
}

type seamEnvelope struct {
	Seam     seamInfo      `json:"seam"`
	Request  SeamRequest   `json:"request"`
	Response *SeamResponse `json:"response,omitempty"`
}

// SeamPreResult is what the panel does before its own handler: answer with Response, or carry on
// with Request (possibly changed).
type SeamPreResult struct {
	Responded bool
	Request   SeamRequest
	Response  SeamResponse
	Failures  int
}

// SeamPostResult is the response the panel sends after its own handler.
type SeamPostResult struct {
	Response SeamResponse
	Changed  bool
	Failures int
}

// SeamTableRow tells the panel where seams exist, so it can skip the call when nobody listens.
type SeamTableRow struct {
	Engine  string   `json:"engine"`
	Route   string   `json:"route"`
	Methods []string `json:"methods,omitempty"`
	Pre     bool     `json:"pre"`
	Post    bool     `json:"post"`
}

type seamTarget struct {
	entry Entry
	seam  Seam
}

type seamCall func(ctx context.Context, e Entry, mode string, in seamEnvelope) (seamReply, error)

var (
	headerNamePattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

	seamForbiddenResponseHeaders = map[string]bool{
		"set-cookie": true, "content-length": true, "transfer-encoding": true, "connection": true, "keep-alive": true,
		"upgrade": true, "content-encoding": true, "trailer": true, "te": true, "host": true,
		"content-security-policy": true, "content-security-policy-report-only": true, "x-frame-options": true,
		"strict-transport-security": true, "referrer-policy": true, "x-content-type-options": true,
		"permissions-policy": true, "clear-site-data": true, "alt-svc": true,
		"access-control-allow-origin": true, "access-control-allow-credentials": true, "access-control-allow-methods": true,
		"access-control-allow-headers": true, "access-control-expose-headers": true,
		"authorization": true, "www-authenticate": true, "proxy-authenticate": true, "proxy-authorization": true,
	}
	seamForbiddenRequestHeaders = map[string]bool{
		"authorization": true, "proxy-authorization": true, "cookie": true, "host": true, "content-length": true,
		"transfer-encoding": true, "connection": true, "keep-alive": true, "upgrade": true, "te": true, "trailer": true,
		"x-csrf-token": true, "x-forwarded-for": true, "x-forwarded-host": true, "x-forwarded-proto": true,
		"x-real-ip": true, "forwarded": true,
	}
)

func forbiddenHeader(forbidden map[string]bool, name string) bool {
	lower := strings.ToLower(name)
	return forbidden[lower] || strings.HasPrefix(lower, "x-lucx-")
}

// cleanHeaders validates a header delta and returns it with canonical names.
func cleanHeaders(in map[string][]string, forbidden map[string]bool) (map[string][]string, error) {
	if len(in) > maxSeamHeaders {
		return nil, fmt.Errorf("more than %d headers", maxSeamHeaders)
	}
	out := make(map[string][]string, len(in))
	for name, values := range in {
		if !headerNamePattern.MatchString(name) {
			return nil, fmt.Errorf("invalid header name %q", name)
		}
		if forbiddenHeader(forbidden, name) {
			return nil, fmt.Errorf("header %s may not be set by a plugin", name)
		}
		for _, v := range values {
			if len(v) > maxSeamHeader || strings.ContainsAny(v, "\r\n\x00") {
				return nil, fmt.Errorf("invalid value for header %s", name)
			}
		}
		out[http.CanonicalHeaderKey(name)] = slices.Clone(values)
	}
	return out, nil
}

func mergeHeaders(base, delta map[string][]string) map[string][]string {
	out := make(map[string][]string, len(base)+len(delta))
	for k, v := range base {
		out[k] = slices.Clone(v)
	}
	for k, v := range delta {
		if len(v) == 0 {
			delete(out, k)
		} else {
			out[k] = slices.Clone(v)
		}
	}
	return out
}

func validStatus(code int) bool { return code >= 200 && code <= 599 }

// applyRequestDelta returns the request as a plugin wants it, or why that is refused.
func applyRequestDelta(req SeamRequest, d *SeamRequestDelta) (SeamRequest, error) {
	if d == nil {
		return req, nil
	}
	headers, err := cleanHeaders(d.Headers, seamForbiddenRequestHeaders)
	if err != nil {
		return req, err
	}
	if d.Body != nil && len(*d.Body) > maxSeamRequest {
		return req, fmt.Errorf("request body larger than %d bytes", maxSeamRequest)
	}
	if d.Query != nil && len(*d.Query) > 8<<10 {
		return req, errors.New("query is too long")
	}
	out := req
	if d.Query != nil {
		out.Query = *d.Query
	}
	if d.Body != nil {
		out.Body = slices.Clone(*d.Body)
	}
	if len(headers) > 0 {
		out.Headers = mergeHeaders(req.Headers, headers)
	}
	return out, nil
}

// applyResponseDelta returns the response with the plugin's changes, or why they are refused.
func applyResponseDelta(base SeamResponse, d *SeamResponseDelta) (SeamResponse, error) {
	if d == nil {
		return base, errors.New("no response given")
	}
	headers, err := cleanHeaders(d.Headers, seamForbiddenResponseHeaders)
	if err != nil {
		return base, err
	}
	out := base
	if d.Status != nil {
		if !validStatus(*d.Status) {
			return base, fmt.Errorf("status %d is out of range", *d.Status)
		}
		out.Status = *d.Status
	}
	if d.Body != nil {
		if len(*d.Body) > maxSeamBody {
			return base, fmt.Errorf("body larger than %d bytes", maxSeamBody)
		}
		out.Body = slices.Clone(*d.Body)
	}
	if len(headers) > 0 {
		out.Headers = mergeHeaders(base.Headers, headers)
	}
	if len(out.Headers) > maxSeamHeaders {
		return base, fmt.Errorf("more than %d headers", maxSeamHeaders)
	}
	return out, nil
}

// sanitizeSeamRequest drops what a plugin must never see, whatever the panel handed over.
func sanitizeSeamRequest(req SeamRequest) SeamRequest {
	out := req
	out.Headers = make(map[string][]string, len(req.Headers))
	for k, v := range req.Headers {
		if !forbiddenHeader(seamForbiddenRequestHeaders, k) {
			out.Headers[k] = v
		}
	}
	return out
}

// runPre asks the before plugins in order (each sees the request as the one before left it; the first
// to answer ends the chain), then the replace plugin. A plugin that fails or answers wrongly is skipped.
func runPre(ctx context.Context, req SeamRequest, before []seamTarget, replace *seamTarget, call seamCall) SeamPreResult {
	res := SeamPreResult{Request: req}
	ask := func(t seamTarget, mode string) (seamReply, bool) {
		reply, err := call(ctx, t.entry, mode, seamEnvelope{Seam: seamInfo{Engine: req.Engine, Mode: mode, Route: t.seam.Route}, Request: res.Request})
		if err != nil {
			res.Failures++
			return seamReply{}, false
		}
		return reply, true
	}
	for _, t := range before {
		reply, ok := ask(t, SeamBefore)
		if !ok {
			continue
		}
		switch reply.Action {
		case seamActionContinue, "":
			next, err := applyRequestDelta(res.Request, reply.Request)
			if err != nil {
				res.Failures++
				logSeamRefusal(t.entry.ID, SeamBefore, err)
				continue
			}
			res.Request = next
		case seamActionRespond:
			resp, err := applyResponseDelta(SeamResponse{}, reply.Response)
			if err != nil || reply.Response == nil || reply.Response.Status == nil {
				res.Failures++
				logSeamRefusal(t.entry.ID, SeamBefore, firstErr(err, errors.New("respond without a status")))
				continue
			}
			res.Responded, res.Response = true, resp
			return res
		default:
			res.Failures++
			logSeamRefusal(t.entry.ID, SeamBefore, fmt.Errorf("unknown action %q", reply.Action))
		}
	}
	if replace == nil {
		return res
	}
	reply, ok := ask(*replace, SeamReplace)
	if !ok {
		return res
	}
	switch reply.Action {
	case seamActionRespond:
		resp, err := applyResponseDelta(SeamResponse{}, reply.Response)
		if err != nil || reply.Response == nil || reply.Response.Status == nil {
			res.Failures++
			logSeamRefusal(replace.entry.ID, SeamReplace, firstErr(err, errors.New("respond without a status")))
			return res
		}
		res.Responded, res.Response = true, resp
	case seamActionFallback, seamActionContinue, "":
	default:
		res.Failures++
		logSeamRefusal(replace.entry.ID, SeamReplace, fmt.Errorf("unknown action %q", reply.Action))
	}
	return res
}

func firstErr(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

// runPost asks the after plugins in order, each seeing the response as the one before left it.
func runPost(ctx context.Context, req SeamRequest, resp SeamResponse, after []seamTarget, call seamCall) SeamPostResult {
	res := SeamPostResult{Response: resp}
	for _, t := range after {
		current := res.Response
		reply, err := call(ctx, t.entry, SeamAfter, seamEnvelope{Seam: seamInfo{Engine: req.Engine, Mode: SeamAfter, Route: t.seam.Route}, Request: req, Response: &current})
		if err != nil {
			res.Failures++
			continue
		}
		switch reply.Action {
		case seamActionKeep, "":
		case seamActionModify:
			next, err := applyResponseDelta(res.Response, reply.Response)
			if err != nil {
				res.Failures++
				logSeamRefusal(t.entry.ID, SeamAfter, err)
				continue
			}
			res.Response, res.Changed = next, true
		default:
			res.Failures++
			logSeamRefusal(t.entry.ID, SeamAfter, fmt.Errorf("unknown action %q", reply.Action))
		}
	}
	return res
}

var seamLogMu sync.Mutex
var seamLastLog = map[string]time.Time{}

// logSeamRefusal logs at most once a minute per plugin and mode: a subscription is fetched constantly.
func logSeamRefusal(id, mode string, err error) {
	seamLogMu.Lock()
	defer seamLogMu.Unlock()
	key := id + "|" + mode
	if time.Since(seamLastLog[key]) < time.Minute {
		return
	}
	seamLastLog[key] = time.Now()
	logx.Warningf("plugins: seam %s of %s refused: %v", mode, id, err)
}

// effectiveSeams are the declared seams the plugin was also granted the permission for.
func effectiveSeams(e Entry) []Seam {
	var out []Seam
	for _, s := range e.Seams {
		if slices.Contains(e.Permissions, s.RequiredPermission()) {
			out = append(out, s)
		}
	}
	return out
}

type seamState struct {
	mu        sync.Mutex
	fails     int
	openUntil time.Time
	inflight  chan struct{}
}

func (s *Supervisor) seamStateFor(id string) *seamState {
	s.seamMu.Lock()
	defer s.seamMu.Unlock()
	st := s.seamStates[id]
	if st == nil {
		st = &seamState{inflight: make(chan struct{}, maxSeamInflight)}
		s.seamStates[id] = st
	}
	return st
}

func (st *seamState) open(now time.Time) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return now.Before(st.openUntil)
}

// ready lists the enabled, running plugins holding seams, by install order, whose breaker is closed.
func (s *Supervisor) seamEntries(count bool) []Entry {
	entries := s.mgr.List()
	slices.SortFunc(entries, func(a, b Entry) int {
		if c := a.InstalledAt.Compare(b.InstalledAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	var out []Entry
	now := time.Now()
	for _, e := range entries {
		if !wantsProcess(e) || len(effectiveSeams(e)) == 0 {
			continue
		}
		if rt, _ := s.Runtime(e.ID); rt.State != StateRunning {
			continue
		}
		if s.seamStateFor(e.ID).open(now) {
			if count {
				s.bump(e.ID, func(st *pluginStats) { st.seamSkipped++ })
			}
			continue
		}
		out = append(out, e)
	}
	return out
}

// seamTargets are the plugins that take part in the request in the given mode: one seam per plugin.
func (s *Supervisor) seamTargets(engine, method, route, mode string) []seamTarget {
	var out []seamTarget
	for _, e := range s.seamEntries(true) {
		for _, sm := range effectiveSeams(e) {
			if sm.Engine == engine && sm.Mode == mode && SeamRouteMatches(engine, sm.Route, route) && sm.SeamMethodMatches(method) {
				out = append(out, seamTarget{entry: e, seam: sm})
				break
			}
		}
	}
	return out
}

// SeamTable lists where seams exist right now (plugins that are not ready are left out).
func (s *Supervisor) SeamTable() []SeamTableRow {
	type key struct{ engine, route, methods string }
	rows := map[key]*SeamTableRow{}
	var order []key
	for _, e := range s.seamEntries(false) {
		for _, sm := range effectiveSeams(e) {
			methods := slices.Clone(sm.Methods)
			slices.Sort(methods)
			k := key{sm.Engine, sm.Route, strings.Join(methods, ",")}
			row := rows[k]
			if row == nil {
				row = &SeamTableRow{Engine: sm.Engine, Route: sm.Route, Methods: methods}
				rows[k] = row
				order = append(order, k)
			}
			if sm.Mode == SeamAfter {
				row.Post = true
			} else {
				row.Pre = true
			}
		}
	}
	out := make([]SeamTableRow, 0, len(order))
	for _, k := range order {
		out = append(out, *rows[k])
	}
	return out
}

// SeamPre runs the before and replace seams for a request.
func (s *Supervisor) SeamPre(ctx context.Context, req SeamRequest) SeamPreResult {
	req = sanitizeSeamRequest(req)
	before := s.seamTargets(req.Engine, req.Method, req.Route, SeamBefore)
	var replace *seamTarget
	if r := s.seamTargets(req.Engine, req.Method, req.Route, SeamReplace); len(r) > 0 {
		replace = &r[0]
	}
	if len(before) == 0 && replace == nil {
		return SeamPreResult{Request: req}
	}
	return runPre(ctx, req, before, replace, s.callSeam)
}

// SeamPost runs the after seams for a response the panel produced.
func (s *Supervisor) SeamPost(ctx context.Context, req SeamRequest, resp SeamResponse) SeamPostResult {
	req = sanitizeSeamRequest(req)
	after := s.seamTargets(req.Engine, req.Method, req.Route, SeamAfter)
	if len(after) == 0 {
		return SeamPostResult{Response: resp}
	}
	return runPost(ctx, req, resp, after, s.callSeam)
}

// callSeam is one call of a plugin's seam endpoint: bounded in time and in concurrency, and counted
// toward the breaker that stops asking a plugin that keeps failing.
func (s *Supervisor) callSeam(ctx context.Context, e Entry, mode string, in seamEnvelope) (seamReply, error) {
	st := s.seamStateFor(e.ID)
	select {
	case st.inflight <- struct{}{}:
		defer func() { <-st.inflight }()
	default:
		s.bump(e.ID, func(p *pluginStats) { p.seamSkipped++ })
		return seamReply{}, errors.New("too many seam calls in flight")
	}
	s.bump(e.ID, func(p *pluginStats) { p.seamCalls++ })
	reply, err := s.askSeam(ctx, e.ID, mode, in)
	st.mu.Lock()
	if err == nil {
		st.fails = 0
	} else {
		st.fails++
		if st.fails >= s.opt.SeamBreakerThreshold {
			st.openUntil = time.Now().Add(s.opt.SeamBreakerCooldown)
			st.fails = s.opt.SeamBreakerThreshold - 1 // one more failure after the cool-down opens it again
		}
	}
	st.mu.Unlock()
	if err != nil {
		s.bump(e.ID, func(p *pluginStats) {
			p.seamErrors++
			p.lastError = "seam " + mode + ": " + err.Error()
		})
		logSeamRefusal(e.ID, mode, err)
	}
	return reply, err
}

func (s *Supervisor) askSeam(ctx context.Context, id, mode string, in seamEnvelope) (seamReply, error) {
	ctx, cancel := context.WithTimeout(ctx, s.opt.SeamTimeout)
	defer cancel()
	body, err := json.Marshal(in)
	if err != nil {
		return seamReply{}, err
	}
	socket, _ := s.runner.Paths(id)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://plugin"+seamPath+mode, bytes.NewReader(body))
	if err != nil {
		return seamReply{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := newSocketClient(socket, s.opt.SeamTimeout).Do(req)
	if err != nil {
		return seamReply{}, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNoContent:
		return seamReply{}, nil
	case http.StatusOK:
	default:
		return seamReply{}, fmt.Errorf("plugin answered HTTP %d", resp.StatusCode)
	}
	limit := int64(maxSeamBody*2 + 1<<20)
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return seamReply{}, err
	}
	if int64(len(raw)) > limit {
		return seamReply{}, errors.New("reply is too large")
	}
	var reply seamReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return seamReply{}, fmt.Errorf("reply is not a seam answer: %w", err)
	}
	return reply, nil
}
