// Copyright (c) 2026 AllIsA1.
// SPDX-License-Identifier: MIT

// Package logx writes the host's log lines to stderr, where systemd collects them.
package logx

import (
	"fmt"
	"log"
)

func Info(args ...any)                    { log.Print("INFO ", fmt.Sprintln(args...)) }
func Infof(format string, args ...any)    { log.Printf("INFO "+format, args...) }
func Warning(args ...any)                 { log.Print("WARN ", fmt.Sprintln(args...)) }
func Warningf(format string, args ...any) { log.Printf("WARN "+format, args...) }
func Error(args ...any)                   { log.Print("ERROR ", fmt.Sprintln(args...)) }
