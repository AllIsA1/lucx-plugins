// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

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
