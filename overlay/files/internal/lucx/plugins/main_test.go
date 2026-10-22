// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

package plugins

import (
	"os"
	"testing"
)

// TestMain lets the test binary stand in for the panel binary: the process
// runner re-executes it as `plugin-exec` to drop privileges.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == ExecCommand {
		os.Exit(RunExec(os.Args[2:]))
	}
	os.Exit(m.Run())
}
