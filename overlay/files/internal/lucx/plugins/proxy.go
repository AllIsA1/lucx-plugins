// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"path"
	"strings"
	"time"
)

const (
	defaultMaxProxyBody = 10 << 20
	proxyResponseWait   = 30 * time.Second
	reservedPrefix      = "/_lucx"
)

var (
	ErrReservedPath       = errors.New("the path belongs to the panel-plugin protocol")
	ErrUpgradeUnsupported = errors.New("protocol upgrades are not proxied")
	ErrNotRunning         = errors.New("the plugin is not running")
)

// isUpgrade is true only when the request names a protocol to switch to. A bare "Connection: upgrade",
// which reverse proxies often add to every request, is not an upgrade.
func isUpgrade(r *http.Request) bool {
	return strings.TrimSpace(r.Header.Get("Upgrade")) != ""
}

// Proxy forwards r to the plugin's unix socket as subpath, on behalf of user.
// Errors are returned before anything is written; once forwarding starts,
// failures are answered here (502, 413).
func (s *Supervisor) Proxy(w http.ResponseWriter, r *http.Request, id, subpath, user string) error {
	e, ok := s.mgr.reg.Get(id)
	if !ok || (e.Type != TypeProcess && e.Type != TypeHook) {
		return ErrNotFound
	}
	clean := path.Clean("/" + subpath)
	if clean == reservedPrefix || strings.HasPrefix(clean, reservedPrefix+"/") {
		return ErrReservedPath
	}
	if isUpgrade(r) {
		return ErrUpgradeUnsupported
	}
	if rt, _ := s.Runtime(id); !e.Enabled || rt.State != StateRunning {
		return ErrNotRunning
	}
	socket, _ := s.runner.Paths(id)
	requestID := newRequestID()

	proxy := &httputil.ReverseProxy{
		Transport: &http.Transport{
			DisableKeepAlives:     true,
			ResponseHeaderTimeout: proxyResponseWait,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socket)
			},
		},
		Rewrite: func(pr *httputil.ProxyRequest) {
			out := pr.Out
			out.URL.Scheme = "http"
			out.URL.Host = "plugin"
			out.URL.Path = clean
			out.URL.RawPath = ""
			out.Host = "plugin"
			for _, h := range []string{"Cookie", "Authorization", "Proxy-Authorization", "X-Csrf-Token"} {
				out.Header.Del(h)
			}
			for name := range out.Header {
				if strings.HasPrefix(strings.ToLower(name), "x-lucx-") {
					out.Header.Del(name)
				}
			}
			pr.SetXForwarded()
			out.Header.Set("X-LucX-User", user)
			out.Header.Set("X-LucX-Plugin-Id", id)
			out.Header.Set("X-LucX-Request-Id", requestID)
		},
		ModifyResponse: func(resp *http.Response) error {
			SetPageHeaders(resp.Header)
			return nil
		},
		ErrorHandler: func(rw http.ResponseWriter, _ *http.Request, err error) {
			status, msg := http.StatusBadGateway, "the plugin did not answer: "+err.Error()
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				status, msg = http.StatusRequestEntityTooLarge, "the request body is too large"
			}
			rw.Header().Set("Content-Type", "application/json")
			rw.WriteHeader(status)
			_ = json.NewEncoder(rw).Encode(map[string]any{"success": false, "msg": msg, "obj": nil})
		},
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.opt.MaxProxyBody)
	proxy.ServeHTTP(w, r)
	return nil
}

func newRequestID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
