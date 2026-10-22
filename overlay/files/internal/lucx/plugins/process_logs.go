// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"bytes"
	"strings"
	"sync"
)

const (
	logRingLines   = 1000
	logLineMaxSize = 4096
)

// lineRing keeps the last lines a plugin wrote; it is an io.Writer so stdout
// and stderr can share it.
type lineRing struct {
	mu      sync.Mutex
	lines   []string
	partial bytes.Buffer
}

func (l *lineRing) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, b := range p {
		if b == '\n' {
			l.push(l.partial.String())
			l.partial.Reset()
			continue
		}
		if l.partial.Len() < logLineMaxSize {
			l.partial.WriteByte(b)
		}
	}
	return len(p), nil
}

func (l *lineRing) push(line string) {
	line = strings.TrimRight(line, "\r")
	if len(l.lines) == logRingLines {
		copy(l.lines, l.lines[1:])
		l.lines = l.lines[:logRingLines-1]
	}
	l.lines = append(l.lines, line)
}

// last returns up to n of the most recent lines, including an unfinished one.
func (l *lineRing) last(n int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	all := l.lines
	if l.partial.Len() > 0 {
		all = append(append([]string(nil), all...), l.partial.String())
	}
	if n < len(all) {
		all = all[len(all)-n:]
	}
	return append([]string(nil), all...)
}
