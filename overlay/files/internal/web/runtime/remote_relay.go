// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var relayStatusSuffix = regexp.MustCompile(`: HTTP (\d{3})(?:: (".*"))?$`)

// RelayPlugin carries one call of the plugin system to a node over the master's own channel to it
// (its credentials, TLS mode and egress rules) and returns the node's answer as it came: the HTTP
// status and the JSON body. A node that refuses is an answer; only an unreachable node is an error.
// It is a function, not a method of Remote, because it does not belong to inbound synchronisation.
func RelayPlugin(ctx context.Context, r *Remote, method, path string, body json.RawMessage) (int, json.RawMessage, error) {
	var payload any
	if len(body) > 0 {
		payload = body
	}
	env, err := r.do(ctx, method, strings.TrimPrefix(path, "/"), payload)
	if err == nil {
		raw, merr := json.Marshal(env)
		return http.StatusOK, raw, merr
	}
	var apiErr *remoteAPIError
	if errors.As(err, &apiErr) && env != nil {
		raw, merr := json.Marshal(env)
		return http.StatusOK, raw, merr
	}
	m := relayStatusSuffix.FindStringSubmatch(err.Error())
	if m == nil {
		return 0, nil, err
	}
	status, _ := strconv.Atoi(m[1])
	msg := "HTTP " + m[1]
	if quoted, uerr := strconv.Unquote(m[2]); uerr == nil && strings.TrimSpace(quoted) != "" {
		msg = strings.TrimSpace(quoted)
		var reply struct {
			Msg string `json:"msg"`
		}
		if json.Unmarshal([]byte(quoted), &reply) == nil && reply.Msg != "" {
			msg = reply.Msg
		}
	}
	raw, merr := json.Marshal(envelope{Success: false, Msg: msg})
	return status, raw, merr
}
