// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import (
	"strings"
	"testing"
	"time"
)

var frameNow = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func TestFrameTokenRoundTripCarriesPluginAndUser(t *testing.T) {
	s := newSigner(t)
	e := tokenEntry()
	for _, user := range []string{"admin", "Ünïcode user", "a.b|c", ""} {
		tok := s.IssueFrame(e, user, frameNow.Add(time.Hour))
		if !strings.HasPrefix(tok, FrameTokenPrefix) {
			t.Fatalf("token %q lacks the %q prefix", tok, FrameTokenPrefix)
		}
		got, gotUser, ok := s.VerifyFrame(tok, frameNow, lookupOf(e))
		if !ok || got.ID != e.ID || gotUser != user {
			t.Errorf("user %q: VerifyFrame = %+v %q %v", user, got, gotUser, ok)
		}
	}
}

func TestFrameTokenExpires(t *testing.T) {
	s := newSigner(t)
	e := tokenEntry()
	tok := s.IssueFrame(e, "admin", frameNow.Add(time.Hour))
	if _, _, ok := s.VerifyFrame(tok, frameNow.Add(time.Hour-time.Second), lookupOf(e)); !ok {
		t.Error("a token must work until it expires")
	}
	for _, at := range []time.Time{frameNow.Add(time.Hour), frameNow.Add(2 * time.Hour)} {
		if _, _, ok := s.VerifyFrame(tok, at, lookupOf(e)); ok {
			t.Errorf("token accepted at %v after it expired", at)
		}
	}
}

func TestFrameTokenDiesWithThePluginBeingOffOrChanged(t *testing.T) {
	s := newSigner(t)
	e := tokenEntry()
	tok := s.IssueFrame(e, "admin", frameNow.Add(time.Hour))

	disabled := e
	disabled.Enabled = false
	changed := e
	changed.Commit = "ffffffffffffffffffffffffffffffffffffffff"
	for name, lookup := range map[string]func(string) (Entry, bool){
		"disabled":  lookupOf(disabled),
		"updated":   lookupOf(changed),
		"uninstall": lookupOf(),
	} {
		if _, _, ok := s.VerifyFrame(tok, frameNow, lookup); ok {
			t.Errorf("%s plugin: token still accepted", name)
		}
	}
}

func TestFrameTokenCannotBeEditedOrReused(t *testing.T) {
	s := newSigner(t)
	e := tokenEntry()
	other := tokenEntry()
	other.ID = "other-plugin"
	tok := s.IssueFrame(e, "admin", frameNow.Add(time.Hour))
	otherTok := s.IssueFrame(other, "admin", frameNow.Add(time.Hour))

	body := strings.TrimPrefix(tok, FrameTokenPrefix)
	payload, mac, _ := strings.Cut(body, ".")
	otherPayload, _, _ := strings.Cut(strings.TrimPrefix(otherTok, FrameTokenPrefix), ".")
	for name, bad := range map[string]string{
		"payload of another plugin": FrameTokenPrefix + otherPayload + "." + mac,
		"flipped payload char":      FrameTokenPrefix + flip(payload) + "." + mac,
		"flipped mac char":          FrameTokenPrefix + payload + "." + flip(mac),
		"truncated mac":             FrameTokenPrefix + payload + "." + mac[:len(mac)-2],
		"no mac":                    FrameTokenPrefix + payload,
		"empty":                     "",
		"prefix only":               FrameTokenPrefix,
		"dots":                      FrameTokenPrefix + "..",
		"not base64":                FrameTokenPrefix + "!!!." + mac,
		"huge":                      FrameTokenPrefix + strings.Repeat("A", 100000) + "." + mac,
	} {
		if _, _, ok := s.VerifyFrame(bad, frameNow, lookupOf(e, other)); ok {
			t.Errorf("%s accepted", name)
		}
	}
	// base64 padding bits: a different spelling of the same signature must not verify.
	if _, _, ok := s.VerifyFrame(FrameTokenPrefix+payload+"."+lastCharVariant(mac), frameNow, lookupOf(e)); ok {
		t.Error("a malleable signature spelling was accepted")
	}
}

func TestFrameAndAPITokensAreNotInterchangeable(t *testing.T) {
	s := newSigner(t)
	e := tokenEntry()
	if _, _, ok := s.VerifyFrame(s.Issue(e), frameNow, lookupOf(e)); ok {
		t.Error("an API token opened a plugin page")
	}
	if _, ok := s.Verify(s.IssueFrame(e, "admin", frameNow.Add(time.Hour)), lookupOf(e)); ok {
		t.Error("a page token authenticated against the panel API")
	}
}

func TestFrameTokensDifferPerKey(t *testing.T) {
	e := tokenEntry()
	tok := newSigner(t).IssueFrame(e, "admin", frameNow.Add(time.Hour))
	if _, _, ok := newSigner(t).VerifyFrame(tok, frameNow, lookupOf(e)); ok {
		t.Error("a token signed with another key was accepted")
	}
}

func flip(s string) string {
	b := []byte(s)
	if b[0] == 'A' {
		b[0] = 'B'
	} else {
		b[0] = 'A'
	}
	return string(b)
}

func lastCharVariant(s string) string {
	// The last base64 char of a 32-byte value carries 4 unused bits; change only those.
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	last := strings.IndexByte(alphabet, s[len(s)-1])
	return s[:len(s)-1] + string(alphabet[last^1])
}
