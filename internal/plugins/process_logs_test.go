// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

package plugins

import (
	"fmt"
	"strings"
	"testing"
)

func TestLineRingKeepsTheNewestLinesAndBoundsLongOnes(t *testing.T) {
	var ring lineRing
	for i := 0; i < logRingLines+50; i++ {
		fmt.Fprintf(&ring, "line %d\r\n", i)
	}
	all := ring.last(logRingLines * 2)
	if len(all) != logRingLines || all[0] != "line 50" || all[len(all)-1] != fmt.Sprintf("line %d", logRingLines+49) {
		t.Errorf("kept %d lines from %q to %q", len(all), all[0], all[len(all)-1])
	}
	_, _ = ring.Write([]byte(strings.Repeat("x", logLineMaxSize*2) + "\nunfinished"))
	tail := ring.last(2)
	if len(tail[0]) != logLineMaxSize || tail[1] != "unfinished" {
		t.Errorf("tail = %d bytes then %q, want a %d-byte cut line then the unfinished one", len(tail[0]), tail[1], logLineMaxSize)
	}
}
