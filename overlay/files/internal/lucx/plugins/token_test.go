// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tokenEntry() Entry {
	return Entry{
		ID: "traffic-alerts", Commit: testCommit, ManifestSHA256: "dc319a19308a4ae62e337927cb3c27e93ef65afa9d36ef75f9ba275b9ab0cadd",
		Enabled: true, Permissions: []string{"api:clients:read", "events:client.created"},
	}
}

func lookupOf(entries ...Entry) func(string) (Entry, bool) {
	return func(id string) (Entry, bool) {
		for _, e := range entries {
			if e.ID == id {
				return e, true
			}
		}
		return Entry{}, false
	}
}

func newSigner(t *testing.T) *TokenSigner {
	t.Helper()
	key, err := LoadOrCreateKey(filepath.Join(t.TempDir(), "plugins.key"))
	if err != nil {
		t.Fatal(err)
	}
	return NewTokenSigner(key)
}

func TestTokenRoundTrip(t *testing.T) {
	s := newSigner(t)
	e := tokenEntry()
	tok := s.Issue(e)
	if !strings.HasPrefix(tok, TokenPrefix) {
		t.Fatalf("token %q lacks the %q prefix the panel uses to route it away from database tokens", tok, TokenPrefix)
	}
	got, ok := s.Verify(tok, lookupOf(e))
	if !ok || got.ID != e.ID {
		t.Fatalf("Verify = %+v, %v", got, ok)
	}
}

func TestTokenIsInvalidatedByAnyChangeToTheBuildOrItsRights(t *testing.T) {
	s := newSigner(t)
	e := tokenEntry()
	tok := s.Issue(e)

	cases := map[string]func(*Entry){
		"new commit":         func(e *Entry) { e.Commit = strings.Repeat("f", 40) },
		"changed manifest":   func(e *Entry) { e.ManifestSHA256 = "0000" },
		"extra permission":   func(e *Entry) { e.Permissions = append(e.Permissions, "api:clients:write") },
		"dropped permission": func(e *Entry) { e.Permissions = e.Permissions[:1] },
		"disabled":           func(e *Entry) { e.Enabled = false },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			changed := e
			changed.Permissions = append([]string(nil), e.Permissions...)
			mutate(&changed)
			if _, ok := s.Verify(tok, lookupOf(changed)); ok {
				t.Error("a token issued for the old build or rights still works")
			}
		})
	}
	reordered := e
	reordered.Permissions = []string{"events:client.created", "api:clients:read", "api:clients:read"}
	if _, ok := s.Verify(tok, lookupOf(reordered)); !ok {
		t.Error("permission order and duplicates must not matter")
	}
}

func TestTokenRejectsForgeriesAndUnknownPlugins(t *testing.T) {
	s := newSigner(t)
	other := newSigner(t)
	e := tokenEntry()
	good := s.Issue(e)

	bad := []string{
		"", "garbage", TokenPrefix, TokenPrefix + "traffic-alerts", TokenPrefix + "traffic-alerts.",
		TokenPrefix + "traffic-alerts.AAAA", other.Issue(e),
		strings.Replace(good, "traffic-alerts", "other-plugin", 1),
		good + "x", strings.ToUpper(good),
	}
	for _, tok := range bad {
		if _, ok := s.Verify(tok, lookupOf(e)); ok {
			t.Errorf("Verify accepted %q", tok)
		}
	}
	if _, ok := s.Verify(good, lookupOf()); ok {
		t.Error("a token for a plugin that is not installed must fail")
	}
}

func TestTokenOfOnePluginDoesNotWorkForAnother(t *testing.T) {
	s := newSigner(t)
	a := tokenEntry()
	b := tokenEntry()
	b.ID = "other-plugin"
	tokA := s.Issue(a)
	swapped := strings.Replace(tokA, a.ID, b.ID, 1)
	if _, ok := s.Verify(swapped, lookupOf(a, b)); ok {
		t.Error("the mac must bind the plugin id")
	}
}

func TestKeyIsCreatedOnceWithPrivatePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "plugins.key")
	first, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 32 {
		t.Fatalf("key length = %d", len(first))
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode = %v, %v; want 0600", st.Mode().Perm(), err)
	}
	again, err := LoadOrCreateKey(path)
	if err != nil || string(again) != string(first) {
		t.Fatalf("the key must survive a restart: %v", err)
	}
	if err := os.WriteFile(path, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateKey(path); err == nil {
		t.Error("a truncated key file must be reported, not silently replaced")
	}
}

// The last base64 character carries two unused bits; a lenient decoder would
// accept all four spellings of the same signature.
func TestTokenSignatureHasASingleCanonicalSpelling(t *testing.T) {
	s := newSigner(t)
	e := tokenEntry()
	good := s.Issue(e)
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	idx := strings.IndexByte(alphabet, good[len(good)-1])
	for v := 0; v < 4; v++ {
		variant := good[:len(good)-1] + string(alphabet[idx&^3+v])
		_, ok := s.Verify(variant, lookupOf(e))
		if (variant == good) != ok {
			t.Errorf("Verify(%q) = %v; only the canonical spelling may verify", variant[len(variant)-4:], ok)
		}
	}
}
