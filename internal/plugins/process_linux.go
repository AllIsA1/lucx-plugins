// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

//go:build linux

package plugins

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

const selfCheckTimeout = 10 * time.Second

// uidSandbox runs every plugin as its own unprivileged user with no_new_privs,
// no capabilities and resource limits. It needs the panel to run as root.
type uidSandbox struct {
	launcher string
	uids     *uidMap

	checkOnce sync.Once
	checkErr  error
}

func newPlatformSandbox(opt processOptions) processSandbox {
	launcher := opt.Launcher
	if launcher == "" {
		launcher, _ = os.Executable()
	}
	return &uidSandbox{launcher: launcher, uids: newUIDMap(filepath.Join(opt.StateRoot, ".uids.json"))}
}

// Check confirms once that this host lets the panel start a process under another user.
func (s *uidSandbox) Check() error {
	s.checkOnce.Do(func() { s.checkErr = s.selfCheck() })
	return s.checkErr
}

func (s *uidSandbox) selfCheck() error {
	if os.Geteuid() != 0 {
		return errors.New("running plugins as child processes needs the panel to run as root, so each plugin can get its own user")
	}
	if s.launcher == "" {
		return errors.New("cannot find the panel executable to launch plugins with")
	}
	ctx, cancel := context.WithTimeout(context.Background(), selfCheckTimeout)
	defer cancel()
	cfg := execConfig{UID: uidBase - 1, GID: uidBase - 1, Parent: os.Getpid(), NProc: 64, NoFile: 64}
	cmd := exec.CommandContext(ctx, s.launcher, append([]string{ExecCommand}, execArgv(cfg)...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("this host does not let plugins run under their own user: %w: %s", err, out)
	}
	return nil
}

func (s *uidSandbox) Isolation() Isolation {
	return NewIsolation("process", false, false, true,
		"each plugin runs as its own system user with no_new_privs, no capabilities, and memory, process and file limits",
		"outgoing connections are not filtered, so the network permission is not enforced",
		"files readable by every user stay readable to plugins",
		"the CPU quota is not applied",
	)
}

func (s *uidSandbox) Own(spec ProcessSpec, p ownedPaths) error {
	uid, err := s.uids.For(spec.ID)
	if err != nil {
		return err
	}
	for _, dir := range []string{spec.CodeDir, filepath.Dir(p.Run)} {
		if err := checkSearchable(dir); err != nil {
			return err
		}
	}
	for _, dir := range []string{p.Run, p.Data} {
		if err := ownDirectory(dir, uid); err != nil {
			return err
		}
	}
	if p.Token != "" {
		return os.Lchown(p.Token, uid, uid)
	}
	return nil
}

// ownDirectory hands a directory the runner just made to the plugin's user. A
// directory that already belongs to someone else is never chowned: that would
// let a plugin steer a root-run walk.
func ownDirectory(dir string, uid int) error {
	st, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot read the owner of %s", dir)
	}
	switch int(owner.Uid) {
	case uid:
		return nil
	case 0:
		return os.Lchown(dir, uid, uid)
	}
	return &fs.PathError{Op: "own", Path: dir, Err: fmt.Errorf("owned by user %d, expected %d", owner.Uid, uid)}
}

func (s *uidSandbox) Prepare(cmd *exec.Cmd, spec ProcessSpec) error {
	uid, err := s.uids.For(spec.ID)
	if err != nil {
		return err
	}
	limits := spec.Limits
	if limits == (ResourceLimits{}) {
		limits = DefaultResourceLimits
	}
	data, err := parseMemoryLimit(limits.MemoryMax)
	if err != nil {
		return err
	}
	cfg := execConfig{UID: uid, GID: uid, Parent: os.Getpid(), DataBytes: data, NProc: uint64(limits.TasksMax), NoFile: 4096, Command: spec.Command, Args: spec.Args}
	cmd.Path = s.launcher
	cmd.Args = append([]string{s.launcher, ExecCommand}, execArgv(cfg)...)
	cmd.Err = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	return nil
}

// Signal targets the whole group: the plugin's pid is its group id.
func (*uidSandbox) Signal(pid int, sig syscall.Signal) { _ = syscall.Kill(-pid, sig) }
