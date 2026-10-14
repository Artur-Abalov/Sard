// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"errors"
	"syscall"
)

// processAlive says whether a process with the pid is running on this host
// (kill with signal 0): a process of another user is alive too. A pid that
// is no process id is not alive, though kill would take 0 or a negative one
// for a process group.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
