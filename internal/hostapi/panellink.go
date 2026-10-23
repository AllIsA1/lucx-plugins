// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package hostapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	headerRelayed    = "X-LucX-Relayed"
	maxPanelResponse = 8 << 20
	panelCallTimeout = 30 * time.Second
)

// PanelLink is the host's own use of the panel's internal API socket (not a plugin's): the node list
// and the relay that carries plugin calls to nodes. Each call names the one route it needs.
type PanelLink struct {
	client *http.Client
}

// NewPanelLink talks to the panel's internal socket at socket.
func NewPanelLink(socket string) *PanelLink {
	return &PanelLink{client: &http.Client{
		Timeout: panelCallTimeout,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socket)
			},
		},
	}}
}

type envelopeReply struct {
	Success bool            `json:"success"`
	Msg     string          `json:"msg"`
	Obj     json.RawMessage `json:"obj"`
}

func (p *PanelLink) do(ctx context.Context, method, path, grant string, body any) (*http.Response, []byte, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://panel"+path, reader)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("X-LucX-Allowed-Routes", grant)
	req.Header.Set("X-LucX-Plugin-Id", "plugin-host")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxPanelResponse+1))
	if err != nil {
		return nil, nil, err
	}
	if len(raw) > maxPanelResponse {
		return nil, nil, errors.New("the panel's answer is too large")
	}
	return resp, raw, nil
}

// messageOf picks the msg of an envelope body, or a short description of the status.
func messageOf(status int, raw []byte) string {
	var env envelopeReply
	if json.Unmarshal(raw, &env) == nil && env.Msg != "" {
		return env.Msg
	}
	if s := string(bytes.TrimSpace(raw)); s != "" && len(s) < 200 {
		return s
	}
	return fmt.Sprintf("HTTP %d", status)
}

// Nodes lists the panel's own nodes; nodes it only knows through another node are not ours to manage.
func (p *PanelLink) Nodes(ctx context.Context) ([]NodeInfo, error) {
	resp, raw, err := p.do(ctx, http.MethodGet, "/panel/api/nodes/list", "GET /nodes/list", nil)
	if err != nil {
		return nil, err
	}
	var env envelopeReply
	if resp.StatusCode != http.StatusOK || json.Unmarshal(raw, &env) != nil || !env.Success {
		return nil, fmt.Errorf("the panel did not list its nodes: %s", messageOf(resp.StatusCode, raw))
	}
	var list []struct {
		ID           int      `json:"id"`
		Name         string   `json:"name"`
		Enable       bool     `json:"enable"`
		Status       string   `json:"status"`
		Transitive   bool     `json:"transitive"`
		NodeFeatures []string `json:"nodeFeatures"`
	}
	if err := json.Unmarshal(env.Obj, &list); err != nil {
		return nil, fmt.Errorf("cannot read the node list: %w", err)
	}
	out := make([]NodeInfo, 0, len(list))
	for _, n := range list {
		if n.Transitive || n.ID < 1 {
			continue
		}
		out = append(out, NodeInfo{ID: n.ID, Name: n.Name, Enable: n.Enable, Status: n.Status, Features: n.NodeFeatures})
	}
	return out, nil
}

// NodeClient reaches one node's plugin system through the panel's relay.
func (p *PanelLink) NodeClient(n NodeInfo) NodePluginsClient {
	return &relayClient{link: p, nodeID: n.ID}
}

type relayClient struct {
	link   *PanelLink
	nodeID int
}

// call relays one request and returns the node's envelope. A refusal by the node is a *NodeRefusal;
// a failure of the panel or of the way to the node is a plain error.
func (c *relayClient) call(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	req := map[string]any{"method": method, "path": path}
	if body != nil {
		req["body"] = body
	}
	resp, raw, err := c.link.do(ctx, http.MethodPost, "/panel/api/lucx/plugin-relay/"+strconv.Itoa(c.nodeID), "POST /lucx/plugin-relay/:nodeId", req)
	if err != nil {
		return nil, err
	}
	if resp.Header.Get(headerRelayed) != "node" {
		return nil, errors.New(messageOf(resp.StatusCode, raw))
	}
	var env envelopeReply
	if resp.StatusCode != http.StatusOK || json.Unmarshal(raw, &env) != nil || !env.Success {
		return nil, &NodeRefusal{Status: resp.StatusCode, Msg: messageOf(resp.StatusCode, raw)}
	}
	return env.Obj, nil
}

func nodePluginPath(parts ...string) string {
	path := "/panel/api/plugins"
	for _, part := range parts {
		path += "/" + url.PathEscape(part)
	}
	return path
}

func (c *relayClient) PluginsList(ctx context.Context) (*NodePluginsState, error) {
	obj, err := c.call(ctx, http.MethodGet, nodePluginPath("list"), nil)
	if err != nil {
		return nil, err
	}
	var st NodePluginsState
	if err := json.Unmarshal(obj, &st); err != nil {
		return nil, fmt.Errorf("decode plugins list: %w", err)
	}
	return &st, nil
}

func (c *relayClient) PluginsInstall(ctx context.Context, req NodePluginInstall) error {
	_, err := c.call(ctx, http.MethodPost, nodePluginPath("remote", "install"), req)
	return err
}

func (c *relayClient) PluginsSetEnabled(ctx context.Context, id string, enabled bool) error {
	action := "disable"
	if enabled {
		action = "enable"
	}
	_, err := c.call(ctx, http.MethodPost, nodePluginPath(action, id), nil)
	return err
}

func (c *relayClient) PluginsUninstall(ctx context.Context, id string, removeData bool) error {
	_, err := c.call(ctx, http.MethodPost, nodePluginPath("uninstall", id), map[string]bool{"removeData": removeData})
	return err
}

func (c *relayClient) PluginsRestart(ctx context.Context, id string) error {
	_, err := c.call(ctx, http.MethodPost, nodePluginPath("restart", id), nil)
	return err
}

func (c *relayClient) PluginsLogs(ctx context.Context, id string, lines int) ([]string, error) {
	obj, err := c.call(ctx, http.MethodGet, nodePluginPath("logs", id)+"?lines="+strconv.Itoa(lines), nil)
	if err != nil {
		return nil, err
	}
	var out []string
	if err := json.Unmarshal(obj, &out); err != nil {
		return nil, fmt.Errorf("decode plugin logs: %w", err)
	}
	return out, nil
}
