// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package refusal

import (
	"errors"
	"os"
	"strconv"
	"syscall"
)

// ErrLockHeld: another init of the repository is running on this host.
var ErrLockHeld = errors.New("repository init is already running")

// OpenFunc is os.OpenFile; the lock opens its file through it so a test can
// refuse a create the way the kernel does for a user without write access.
type OpenFunc func(name string, flag int, perm os.FileMode) (*os.File, error)

// Lock takes an exclusive lock on path (В8) and returns the function that
// releases it and removes the file. flock(2) goes away with the process
// however it ends, so the file of a dead process never blocks anyone.
func Lock(open OpenFunc, path string) (unlock func(), err error) {
	f, err := open(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLockHeld
		}
		return nil, err
	}
	// A holder that just released may have removed the file this lock is
	// on; the lock counts only if the file is still the one at path.
	if !sameFile(f, path) {
		_ = f.Close()
		return nil, ErrLockHeld
	}
	_, _ = f.WriteString(strconv.Itoa(os.Getpid()) + "\n")
	return func() {
		_ = os.Remove(path)
		_ = f.Close()
	}, nil
}

func sameFile(f *os.File, path string) bool {
	held, err := f.Stat()
	if err != nil {
		return false
	}
	current, err := os.Stat(path)
	return err == nil && os.SameFile(held, current)
}
