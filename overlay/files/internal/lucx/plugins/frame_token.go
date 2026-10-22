// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

// FrameTokenTTL is how long a page token opens a plugin's pages; assets load with the page.
const FrameTokenTTL = 2 * time.Hour

const (
	// FrameTokenPrefix marks a page token: the sandboxed frame has no cookies, so its URL carries this.
	FrameTokenPrefix = "lxf."

	frameTokenContext = "lucx-plugin-frame-v1"
	maxFrameTokenLen  = 1024
)

type framePayload struct {
	Plugin  string `json:"i"`
	User    string `json:"u"`
	Expires int64  `json:"e"`
}

func (s *TokenSigner) frameMAC(payload string, e Entry) []byte {
	h := hmac.New(sha256.New, s.key)
	h.Write([]byte(frameTokenContext))
	for _, part := range []string{payload, Fingerprint(e)} {
		h.Write([]byte{0})
		h.Write([]byte(part))
	}
	return h.Sum(nil)
}

// IssueFrame returns a token that lets one plugin's pages load without a session until expires.
func (s *TokenSigner) IssueFrame(e Entry, user string, expires time.Time) string {
	raw, _ := json.Marshal(framePayload{Plugin: e.ID, User: user, Expires: expires.Unix()})
	payload := base64.RawURLEncoding.EncodeToString(raw)
	return FrameTokenPrefix + payload + "." + base64.RawURLEncoding.EncodeToString(s.frameMAC(payload, e))
}

// VerifyFrame checks a page token against the current registry: the plugin must be enabled and unchanged.
func (s *TokenSigner) VerifyFrame(token string, now time.Time, lookup func(id string) (Entry, bool)) (Entry, string, bool) {
	rest, ok := strings.CutPrefix(token, FrameTokenPrefix)
	if !ok || len(token) > maxFrameTokenLen {
		return Entry{}, "", false
	}
	payload, sig, ok := strings.Cut(rest, ".")
	if !ok {
		return Entry{}, "", false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(payload)
	if err != nil {
		return Entry{}, "", false
	}
	var p framePayload
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil || !idPattern.MatchString(p.Plugin) || now.Unix() >= p.Expires {
		return Entry{}, "", false
	}
	e, found := lookup(p.Plugin)
	if !found || !e.Enabled {
		return Entry{}, "", false
	}
	presented, err := base64.RawURLEncoding.Strict().DecodeString(sig)
	if err != nil || !hmac.Equal(presented, s.frameMAC(payload, e)) {
		return Entry{}, "", false
	}
	return e, p.User, true
}

// IssueFrameToken mints the token the panel UI puts in a plugin page's URL for user.
func (m *Manager) IssueFrameToken(id, user string) (string, time.Time, error) {
	e, ok := m.reg.Get(id)
	if !ok || !runsOnThisPanel(e) || e.Type == TypeHook || (len(e.UI.Menu) == 0 && len(e.UI.Replaces) == 0) {
		return "", time.Time{}, ErrNotFound
	}
	if !e.Enabled {
		return "", time.Time{}, ErrNotRunning
	}
	expires := m.cfg.Now().Add(FrameTokenTTL)
	return m.signer.IssueFrame(e, user, expires), expires, nil
}

// VerifyFrameToken resolves a page token to its plugin and the user it was issued for.
func (m *Manager) VerifyFrameToken(token string) (Entry, string, bool) {
	return m.signer.VerifyFrame(token, m.cfg.Now(), m.reg.Get)
}
