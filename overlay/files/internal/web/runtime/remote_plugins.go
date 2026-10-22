// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// RemotePluginRuntime is the live state of a plugin process on a node.
type RemotePluginRuntime struct {
	State          string `json:"state"`
	Healthy        bool   `json:"healthy"`
	PID            int    `json:"pid"`
	Restarts       int    `json:"restarts"`
	HealthError    string `json:"healthError,omitempty"`
	Detail         string `json:"detail,omitempty"`
	LastError      string `json:"lastError,omitempty"`
	EventsSent     int    `json:"eventsSent,omitempty"`
	EventsFailed   int    `json:"eventsFailed,omitempty"`
	EventsDropped  int    `json:"eventsDropped,omitempty"`
	PipelineErrors int    `json:"pipelineErrors,omitempty"`
}

// RemotePlugin is one plugin installed on a node.
type RemotePlugin struct {
	ID              string               `json:"id"`
	Name            string               `json:"name"`
	Version         string               `json:"version"`
	Type            string               `json:"type"`
	Scope           string               `json:"scope"`
	Commit          string               `json:"commit"`
	ManifestSHA256  string               `json:"manifestSha256,omitempty"`
	Enabled         bool                 `json:"enabled"`
	DisabledReason  string               `json:"disabledReason,omitempty"`
	ManagedByMaster bool                 `json:"managedByMaster,omitempty"`
	Permissions     []string             `json:"permissions"`
	Runtime         *RemotePluginRuntime `json:"runtime,omitempty"`
}

// RemotePluginsState is what a node reports about its plugin system.
type RemotePluginsState struct {
	Available             bool           `json:"available"`
	AllowMasterManagement bool           `json:"allowMasterManagement"`
	ProcessesAvailable    bool           `json:"processesAvailable"`
	ProcessesError        string         `json:"processesError"`
	SafeMode              bool           `json:"safeMode"`
	Error                 string         `json:"error"`
	Plugins               []RemotePlugin `json:"plugins"`
}

// RemotePluginInstall is what a master approved: one commit, one manifest and
// exactly these permissions. The node downloads and verifies it itself.
type RemotePluginInstall struct {
	URL            string   `json:"url"`
	Commit         string   `json:"commit"`
	ManifestSHA256 string   `json:"manifestSha256"`
	Permissions    []string `json:"permissions"`
}

// RemotePluginError is a node's own refusal of a plugin call, in the node's words.
type RemotePluginError struct {
	Status int
	Msg    string
}

func (e *RemotePluginError) Error() string { return e.Msg }

var httpStatusSuffix = regexp.MustCompile(`: HTTP (\d{3})(?:: (".*"))?$`)

// pluginDo is do with an HTTP refusal turned from a quoted body dump into the node's message.
func (r *Remote) pluginDo(ctx context.Context, method, path string, body any) (*envelope, error) {
	env, err := r.do(ctx, method, path, body)
	if err == nil {
		return env, nil
	}
	m := httpStatusSuffix.FindStringSubmatch(err.Error())
	if m == nil {
		return env, err
	}
	status, _ := strconv.Atoi(m[1])
	msg := fmt.Sprintf("HTTP %d", status)
	if body, uerr := strconv.Unquote(m[2]); uerr == nil && strings.TrimSpace(body) != "" {
		msg = strings.TrimSpace(body)
		var reply struct {
			Msg string `json:"msg"`
		}
		if json.Unmarshal([]byte(body), &reply) == nil && reply.Msg != "" {
			msg = reply.Msg
		}
	}
	return env, &RemotePluginError{Status: status, Msg: msg}
}

func pluginPath(parts ...string) string {
	path := "panel/api/plugins"
	for _, p := range parts {
		path += "/" + url.PathEscape(p)
	}
	return path
}

// PluginsList reads the node's plugin state.
func (r *Remote) PluginsList(ctx context.Context) (*RemotePluginsState, error) {
	env, err := r.pluginDo(ctx, http.MethodGet, pluginPath("list"), nil)
	if err != nil {
		return nil, err
	}
	var state RemotePluginsState
	if err := json.Unmarshal(env.Obj, &state); err != nil {
		return nil, fmt.Errorf("decode plugins list: %w", err)
	}
	return &state, nil
}

// PluginsInstall asks the node to install exactly the approved plugin, disabled.
func (r *Remote) PluginsInstall(ctx context.Context, req RemotePluginInstall) error {
	_, err := r.pluginDo(ctx, http.MethodPost, pluginPath("remote", "install"), req)
	return err
}

// PluginsSetEnabled turns a plugin the master installed on or off.
func (r *Remote) PluginsSetEnabled(ctx context.Context, id string, enabled bool) error {
	action := "disable"
	if enabled {
		action = "enable"
	}
	_, err := r.pluginDo(ctx, http.MethodPost, pluginPath(action, id), nil)
	return err
}

// PluginsUninstall removes a plugin the master installed, optionally with its data.
func (r *Remote) PluginsUninstall(ctx context.Context, id string, removeData bool) error {
	_, err := r.pluginDo(ctx, http.MethodPost, pluginPath("uninstall", id), map[string]bool{"removeData": removeData})
	return err
}

// PluginsRestart restarts a plugin process the master installed.
func (r *Remote) PluginsRestart(ctx context.Context, id string) error {
	_, err := r.pluginDo(ctx, http.MethodPost, pluginPath("restart", id), nil)
	return err
}

// PluginsLogs returns the last lines a plugin process wrote on the node.
func (r *Remote) PluginsLogs(ctx context.Context, id string, lines int) ([]string, error) {
	env, err := r.pluginDo(ctx, http.MethodGet, pluginPath("logs", id)+"?lines="+strconv.Itoa(lines), nil)
	if err != nil {
		return nil, err
	}
	var out []string
	if err := json.Unmarshal(env.Obj, &out); err != nil {
		return nil, fmt.Errorf("decode plugin logs: %w", err)
	}
	return out, nil
}
