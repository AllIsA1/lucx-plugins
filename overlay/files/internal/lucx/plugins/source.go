// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// DefaultArchiveBase serves github.com repository tarballs by commit or ref.
const DefaultArchiveBase = "https://codeload.github.com"

const maxRefLen = 200

var (
	ownerPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,38}$`)
	repoPattern   = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	refPartRegexp = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	shaPattern    = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// Source identifies a plugin repository on github.com; an empty Ref means HEAD.
type Source struct {
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
	Ref   string `json:"ref,omitempty"`
}

// ParseSource accepts only https://github.com/<owner>/<repo>[/tree/<ref>] links.
func ParseSource(raw string) (Source, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return Source{}, fmt.Errorf("invalid plugin URL: %w", err)
	}
	if u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.Port() != "" ||
		!strings.EqualFold(u.Hostname(), "github.com") {
		return Source{}, errors.New("only https://github.com/<owner>/<repo> links are supported")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(u.EscapedPath(), "%") {
		return Source{}, errors.New("plugin URL must not contain a query, fragment or escapes")
	}
	path := strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), "/")
	parts := strings.Split(path, "/")
	for _, p := range parts {
		if p == "" {
			return Source{}, errors.New("plugin URL has an empty path segment")
		}
	}
	if len(parts) < 2 {
		return Source{}, errors.New("plugin URL must name an owner and a repository")
	}
	src := Source{Owner: parts[0], Repo: strings.TrimSuffix(parts[1], ".git")}
	if !ownerPattern.MatchString(src.Owner) {
		return Source{}, fmt.Errorf("invalid repository owner %q", src.Owner)
	}
	if !repoPattern.MatchString(src.Repo) || src.Repo == "." || src.Repo == ".." {
		return Source{}, fmt.Errorf("invalid repository name %q", src.Repo)
	}
	if len(parts) == 2 {
		return src, nil
	}
	if parts[2] != "tree" || len(parts) < 4 {
		return Source{}, errors.New("only /tree/<ref> may follow the repository name")
	}
	for _, p := range parts[3:] {
		if !refPartRegexp.MatchString(p) || p == "." || p == ".." || strings.HasPrefix(p, "-") {
			return Source{}, fmt.Errorf("invalid ref segment %q", p)
		}
	}
	src.Ref = strings.Join(parts[3:], "/")
	if len(src.Ref) > maxRefLen {
		return Source{}, errors.New("ref is too long")
	}
	return src, nil
}

// String renders the canonical GitHub link, the inverse of ParseSource.
func (s Source) String() string {
	out := "https://github.com/" + s.Owner + "/" + s.Repo
	if s.Ref != "" {
		out += "/tree/" + s.Ref
	}
	return out
}

// ArchiveURL builds the tar.gz download URL below an already validated base.
func (s Source) ArchiveURL(base string) string {
	ref := s.Ref
	if ref == "" {
		ref = "HEAD"
	}
	return strings.TrimRight(base, "/") + "/" + s.Owner + "/" + s.Repo + "/tar.gz/" + ref
}

// ValidateArchiveBase normalizes a mirror base URL; empty selects the default.
// Plain http is accepted only for loopback hosts.
func ValidateArchiveBase(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultArchiveBase, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" {
		return "", errors.New("archive base must be an absolute https URL")
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", errors.New("archive base must not contain credentials, a query or a fragment")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopbackHost(u.Hostname()) {
			return "", errors.New("archive base must use https (plain http is allowed for loopback only)")
		}
	default:
		return "", errors.New("archive base must be an absolute https URL")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
