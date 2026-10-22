// Copyright (c) 2026 AllIsA1.
// Licensed under the PolyForm Noncommercial License 1.0.0.
// LucX plugin system component, independent of LucX-UI. Free for personal and educational use.
// Commercial use (including VPN resale) requires explicit written permission from the author.
// SPDX-License-Identifier: PolyForm-Noncommercial-1.0.0

//go:build linux

package plugins

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const lastCapFallback = 63

// execConfig is what the privileged launcher applies before it execs a plugin.
type execConfig struct {
	UID, GID  int
	Parent    int
	DataBytes uint64
	NProc     uint64
	NoFile    uint64
	Command   string
	Args      []string
}

// execArgv renders cfg as the arguments after `plugin-exec`.
func execArgv(cfg execConfig) []string {
	argv := []string{
		"--uid", strconv.Itoa(cfg.UID), "--gid", strconv.Itoa(cfg.GID), "--parent", strconv.Itoa(cfg.Parent),
		"--data-bytes", strconv.FormatUint(cfg.DataBytes, 10),
		"--nproc", strconv.FormatUint(cfg.NProc, 10), "--nofile", strconv.FormatUint(cfg.NoFile, 10),
	}
	if cfg.Command == "" {
		return argv
	}
	argv = append(argv, "--", cfg.Command)
	return append(argv, cfg.Args...)
}

func parseExecArgs(args []string) (execConfig, error) {
	var cfg execConfig
	seen := map[string]bool{}
	i := 0
	for ; i < len(args) && args[i] != "--"; i += 2 {
		if i+1 >= len(args) {
			return cfg, fmt.Errorf("%s needs a value", args[i])
		}
		n, err := strconv.ParseUint(args[i+1], 10, 62)
		if err != nil {
			return cfg, fmt.Errorf("%s: %q is not a non-negative number", args[i], args[i+1])
		}
		switch args[i] {
		case "--uid":
			cfg.UID = int(n)
		case "--gid":
			cfg.GID = int(n)
		case "--parent":
			cfg.Parent = int(n)
		case "--data-bytes":
			cfg.DataBytes = n
		case "--nproc":
			cfg.NProc = n
		case "--nofile":
			cfg.NoFile = n
		default:
			return cfg, fmt.Errorf("unknown option %s", args[i])
		}
		seen[args[i]] = true
	}
	if cfg.UID == 0 || cfg.GID == 0 {
		return cfg, errors.New("--uid and --gid are required and must not be 0: plugins never run as root")
	}
	if !seen["--parent"] || cfg.Parent == 0 {
		return cfg, errors.New("--parent is required")
	}
	if i < len(args) && i+1 < len(args) {
		cfg.Command, cfg.Args = args[i+1], args[i+2:]
	}
	return cfg, nil
}

// parseMemoryLimit reads a systemd-style size: plain bytes or a K, M, G suffix.
// "infinity" means no limit and yields 0.
func parseMemoryLimit(v string) (uint64, error) {
	if v == "infinity" {
		return 0, nil
	}
	mult := uint64(1)
	switch {
	case strings.HasSuffix(v, "K"):
		mult, v = 1<<10, strings.TrimSuffix(v, "K")
	case strings.HasSuffix(v, "M"):
		mult, v = 1<<20, strings.TrimSuffix(v, "M")
	case strings.HasSuffix(v, "G"):
		mult, v = 1<<30, strings.TrimSuffix(v, "G")
	}
	n, err := strconv.ParseUint(v, 10, 40)
	if err != nil {
		return 0, fmt.Errorf("invalid memory limit %q", v)
	}
	return n * mult, nil
}

// RunExec is the `plugin-exec` subcommand. It runs as root inside the child
// the panel just forked, confines the process and then replaces itself with the
// plugin. It returns only on failure, with the exit status to use.
func RunExec(args []string) int {
	cfg, err := parseExecArgs(args)
	if err != nil {
		return execFail(err)
	}
	// Capability and no_new_privs state is per thread; the exec must happen on this one.
	runtime.LockOSThread()
	if err := confine(cfg); err != nil {
		return execFail(err)
	}
	if cfg.Command == "" {
		return 0
	}
	argv := append([]string{cfg.Command}, cfg.Args...)
	return execFail(syscall.Exec(cfg.Command, argv, os.Environ()))
}

func execFail(err error) int {
	fmt.Fprintln(os.Stderr, "plugin-exec:", err)
	return 126
}

func confine(cfg execConfig) error {
	for _, l := range []struct {
		res int
		val uint64
	}{{unix.RLIMIT_NPROC, cfg.NProc}, {unix.RLIMIT_NOFILE, cfg.NoFile}, {unix.RLIMIT_CORE, 0}} {
		if l.val == 0 && l.res != unix.RLIMIT_CORE {
			continue
		}
		if err := unix.Setrlimit(l.res, &unix.Rlimit{Cur: l.val, Max: l.val}); err != nil {
			return fmt.Errorf("setrlimit %d: %w", l.res, err)
		}
	}
	if cfg.DataBytes > 0 {
		if err := unix.Setrlimit(unix.RLIMIT_DATA, &unix.Rlimit{Cur: cfg.DataBytes, Max: cfg.DataBytes}); err != nil {
			return fmt.Errorf("setrlimit data: %w", err)
		}
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("no_new_privs: %w", err)
	}
	if err := dropBoundingSet(); err != nil {
		return err
	}
	// Ambient capabilities need a recent kernel; without them there is nothing to clear.
	_ = unix.Prctl(unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_CLEAR_ALL, 0, 0, 0)
	syscall.Umask(0o077)
	if err := syscall.Setgroups(nil); err != nil {
		return fmt.Errorf("setgroups: %w", err)
	}
	if err := syscall.Setgid(cfg.GID); err != nil {
		return fmt.Errorf("setgid: %w", err)
	}
	if err := syscall.Setuid(cfg.UID); err != nil {
		return fmt.Errorf("setuid: %w", err)
	}
	if syscall.Setuid(0) == nil {
		return errors.New("privileges can be regained after setuid")
	}
	// A credential change clears the parent-death signal, so it is armed again here.
	if err := unix.Prctl(unix.PR_SET_PDEATHSIG, uintptr(syscall.SIGKILL), 0, 0, 0); err != nil {
		return fmt.Errorf("pdeathsig: %w", err)
	}
	if os.Getppid() != cfg.Parent {
		return errors.New("the panel is gone")
	}
	return nil
}

func dropBoundingSet() error {
	last := lastCapFallback
	if raw, err := os.ReadFile("/proc/sys/kernel/cap_last_cap"); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
			last = n
		}
	}
	for c := 0; c <= last; c++ {
		if err := unix.Prctl(unix.PR_CAPBSET_DROP, uintptr(c), 0, 0, 0); err != nil && !errors.Is(err, unix.EINVAL) {
			return fmt.Errorf("drop capability %d: %w", c, err)
		}
	}
	return nil
}
