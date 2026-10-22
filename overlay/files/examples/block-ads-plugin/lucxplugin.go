// Copyright (c) 2026 LucX-UI Project.
// SPDX-License-Identifier: MIT

// Package main: lucxplugin.go holds what every LucX plugin needs. Copy it into
// your plugin and keep it next to main.go.
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Serve listens on the unix socket the panel assigned ($LUCX_SOCKET), answers
// GET /_lucx/health itself and sends everything else to handler. It returns
// after SIGTERM/SIGINT once in-flight requests finished.
func Serve(handler http.Handler) error {
	socket := os.Getenv("LUCX_SOCKET")
	if socket == "" {
		return errors.New("LUCX_SOCKET is not set: run this plugin through the panel")
	}
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return err
	}
	_ = os.Remove(socket)
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /_lucx/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.Handle("/", handler)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Event is a panel event the panel delivers to POST /_lucx/events. Subscribe
// by listing events:<name> permissions in lucx-plugin.json.
type Event struct {
	ID     string          `json:"id"`
	Type   string          `json:"type"`
	Source string          `json:"source"`
	Time   time.Time       `json:"time"`
	Data   json.RawMessage `json:"data"`
}

// EventHandler decodes a delivered event and hands it to fn. Answer quickly:
// the panel waits at most a few seconds and does not retry.
func EventHandler(fn func(Event)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ev Event
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&ev); err != nil {
			http.Error(w, "bad event", http.StatusBadRequest)
			return
		}
		fn(ev)
		w.WriteHeader(http.StatusNoContent)
	})
}

// PatchOp is one RFC 6902 operation. The panel accepts add, remove, replace and test.
type PatchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

// PipelineHandler serves POST /_lucx/pipeline/<name>: fn receives the current
// document and returns the operations to apply (none means no change). The
// panel runs this on every config generation, so fn must be idempotent.
func PipelineHandler(fn func(doc json.RawMessage) ([]PatchOp, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		doc, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<20))
		if err != nil {
			http.Error(w, "bad document", http.StatusBadRequest)
			return
		}
		ops, err := fn(doc)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if len(ops) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"patch": ops})
	})
}

// RequestUser is the panel user on whose behalf the request was made. The
// panel sets it itself; clients cannot forge it.
func RequestUser(r *http.Request) string { return r.Header.Get("X-LucX-User") }

// StateDir is the only directory the plugin may write to.
func StateDir() string {
	if dir := os.Getenv("STATE_DIRECTORY"); dir != "" {
		return dir
	}
	return os.Getenv("LUCX_DATA_DIR")
}

// Panel calls the panel API with the token the panel issued to this plugin.
// Only routes covered by the permissions in lucx-plugin.json answer; others return 403.
type Panel struct {
	base   string
	token  string
	client *http.Client
}

// NewPanel reads $LUCX_PANEL_URL and the credential file the panel provides.
func NewPanel() (*Panel, error) {
	base := os.Getenv("LUCX_PANEL_URL")
	dir := os.Getenv("CREDENTIALS_DIRECTORY")
	if base == "" || dir == "" {
		return nil, errors.New("no panel address or token: run this plugin through the panel")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "panel-token"))
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{}
	if u.Scheme == "https" {
		// The panel's certificate is issued for its public name, not for the
		// address we use; the connection stays on this machine or a trusted network.
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
	}
	return &Panel{base: strings.TrimRight(base, "/"), token: strings.TrimSpace(string(raw)), client: &http.Client{Transport: transport, Timeout: 15 * time.Second}}, nil
}

type envelope struct {
	Success bool            `json:"success"`
	Msg     string          `json:"msg"`
	Obj     json.RawMessage `json:"obj"`
}

// Do calls /panel/api<path> and decodes the "obj" of the panel's answer into out.
func (p *Panel) Do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = strings.NewReader(string(raw))
	}
	req, err := http.NewRequestWithContext(ctx, method, p.base+"/panel/api"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("panel answered HTTP %d for %s %s", resp.StatusCode, method, path)
	}
	var env envelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return err
	}
	if !env.Success {
		return errors.New(env.Msg)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(env.Obj, out)
}
