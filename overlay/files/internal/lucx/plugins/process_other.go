// Copyright (c) 2026 LucX-UI Project.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX-UI Component. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

//go:build !linux

package plugins

import (
	"errors"
	"os/exec"
	"syscall"
)

type unsupportedSandbox struct{}

func newPlatformSandbox(processOptions) processSandbox { return unsupportedSandbox{} }

func (unsupportedSandbox) Check() error {
	return errors.New("the process runner is only implemented for Linux")
}

func (unsupportedSandbox) Isolation() Isolation { return NewIsolation("none", false, false, false) }

func (unsupportedSandbox) Own(ProcessSpec, ownedPaths) error {
	return errors.New("unsupported platform")
}

func (unsupportedSandbox) Prepare(*exec.Cmd, ProcessSpec) error {
	return errors.New("unsupported platform")
}

func (unsupportedSandbox) Signal(int, syscall.Signal) {}

// RunExec exists only on Linux, where the process runner needs it.
func RunExec([]string) int { return 126 }
