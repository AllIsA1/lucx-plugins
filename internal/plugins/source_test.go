// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import "testing"

func TestParseSourceValid(t *testing.T) {
	cases := []struct {
		raw  string
		want Source
	}{
		{"https://github.com/acme/alerts", Source{Owner: "acme", Repo: "alerts"}},
		{"https://github.com/acme/alerts/", Source{Owner: "acme", Repo: "alerts"}},
		{"https://github.com/acme/alerts.git", Source{Owner: "acme", Repo: "alerts"}},
		{"https://GitHub.com/acme/alerts", Source{Owner: "acme", Repo: "alerts"}},
		{"https://github.com/acme/alerts/tree/v1.2.0", Source{Owner: "acme", Repo: "alerts", Ref: "v1.2.0"}},
		{"https://github.com/acme/alerts/tree/feature/x-y", Source{Owner: "acme", Repo: "alerts", Ref: "feature/x-y"}},
		{"  https://github.com/a_b/c.d-e  ", Source{Owner: "a_b", Repo: "c.d-e"}},
	}
	for _, tc := range cases {
		got, err := ParseSource(tc.raw)
		if err != nil {
			t.Errorf("ParseSource(%q): %v", tc.raw, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseSource(%q) = %+v, want %+v", tc.raw, got, tc.want)
		}
	}
}

func TestParseSourceRejects(t *testing.T) {
	cases := []string{
		"",
		"http://github.com/acme/alerts",
		"ftp://github.com/acme/alerts",
		"https://gitlab.com/acme/alerts",
		"https://github.com.evil.com/acme/alerts",
		"https://evil.com/github.com/acme/alerts",
		"https://user:pw@github.com/acme/alerts",
		"https://github.com:8443/acme/alerts",
		"https://github.com/acme",
		"https://github.com/acme/alerts?x=1",
		"https://github.com/acme/alerts#frag",
		"https://github.com/acme/alerts/blob/main/README.md",
		"https://github.com/acme/alerts/releases",
		"https://github.com/acme/alerts/tree/",
		"https://github.com/acme/alerts/tree/../../x",
		"https://github.com/../alerts",
		"https://github.com/acme/..",
		"https://github.com/acme/al erts",
		"https://github.com/acme/alerts/tree/a%2F..%2Fb",
		"https://github.com/ac%2Fme/alerts",
		"git@github.com:acme/alerts.git",
		"https://github.com//alerts",
	}
	for _, raw := range cases {
		if got, err := ParseSource(raw); err == nil {
			t.Errorf("ParseSource(%q) accepted: %+v", raw, got)
		}
	}
}

func TestSourceArchiveURL(t *testing.T) {
	cases := []struct {
		src  Source
		base string
		want string
	}{
		{Source{Owner: "acme", Repo: "alerts"}, "https://codeload.github.com", "https://codeload.github.com/acme/alerts/tar.gz/HEAD"},
		{Source{Owner: "acme", Repo: "alerts", Ref: "v1"}, "https://codeload.github.com/", "https://codeload.github.com/acme/alerts/tar.gz/v1"},
		{Source{Owner: "acme", Repo: "alerts", Ref: "feature/x"}, "https://mirror.example/gh", "https://mirror.example/gh/acme/alerts/tar.gz/feature/x"},
	}
	for _, tc := range cases {
		if got := tc.src.ArchiveURL(tc.base); got != tc.want {
			t.Errorf("ArchiveURL = %q, want %q", got, tc.want)
		}
	}
}

func TestSourceString(t *testing.T) {
	if got := (Source{Owner: "a", Repo: "b"}).String(); got != "https://github.com/a/b" {
		t.Errorf("String = %q", got)
	}
	if got := (Source{Owner: "a", Repo: "b", Ref: "dev"}).String(); got != "https://github.com/a/b/tree/dev" {
		t.Errorf("String = %q", got)
	}
}

func TestValidateArchiveBase(t *testing.T) {
	ok := []string{
		"", "https://codeload.github.com", "https://mirror.example/gh/",
		"http://127.0.0.1:8080", "http://localhost:9000/x", "http://[::1]:7000",
	}
	for _, s := range ok {
		if _, err := ValidateArchiveBase(s); err != nil {
			t.Errorf("ValidateArchiveBase(%q): %v", s, err)
		}
	}
	bad := []string{
		"http://mirror.example", "ftp://x", "https://", "https://u:p@x.example",
		"https://x.example?a=1", "https://x.example#f", "not a url", "//x.example",
	}
	for _, s := range bad {
		if got, err := ValidateArchiveBase(s); err == nil {
			t.Errorf("ValidateArchiveBase(%q) accepted: %q", s, got)
		}
	}
	if got, _ := ValidateArchiveBase("https://mirror.example/gh/"); got != "https://mirror.example/gh" {
		t.Errorf("trailing slash not trimmed: %q", got)
	}
	if got, _ := ValidateArchiveBase(""); got != DefaultArchiveBase {
		t.Errorf("empty must resolve to default, got %q", got)
	}
}
