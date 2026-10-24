// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package service

import (
	"fmt"
	"regexp"
	"strings"
)

// externalLinkMaxLength is the longest external link taken, in bytes.
const externalLinkMaxLength = 8 << 10

var externalLinkShape = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.\-]*://[^\s\x00-\x1f\x7f]+$`)

// validateExternalShareLink says whether value can be a client's external link. Any scheme is fine, because
// the panel hands such a link to the client unchanged and skips it in the JSON and Clash formats, which it
// can only build for the schemes it knows. What is refused is a value that could damage the subscription it
// is written into: one that is not a single line "scheme://…", or contains blanks or control characters
// (a line break would let a link carry a line of its own).
func validateExternalShareLink(value string) error {
	value = strings.TrimSpace(value)
	switch {
	case len(value) > externalLinkMaxLength:
		return fmt.Errorf("longer than %d bytes", externalLinkMaxLength)
	case !externalLinkShape.MatchString(value):
		return fmt.Errorf("not a single-line link of the form scheme://…")
	}
	return nil
}
