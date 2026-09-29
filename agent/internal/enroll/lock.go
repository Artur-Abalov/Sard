// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package enroll

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

const lockSuffix = ".sard-enroll.lock"

// LockPath is where Lock puts its lock file: next to certFile (В15).
func LockPath(certFile string) string {
	return filepath.Join(filepath.Dir(certFile), filepath.Base(certFile)+lockSuffix)
}

// Lock refuses a second concurrent "enroll" for the same config (В15): an
// exclusive, non-blocking flock(2) on a file next to certFile, which
// returns a function that removes the file and releases the lock. A
// second Lock call while the first is held fails with a ClassTemporary
// *Error before either command talks to the server.
//
// flock, not a plain O_CREATE|O_EXCL file (F5, В15): the kernel releases
// an flock the moment the holding process exits for any reason, crash
// included, so a leftover lock file from a dead process never wedges
// every later enroll behind "an enrollment is already in progress"
// forever — a fresh Lock call reuses or replaces the same file and
// acquires the lock straight away.
func Lock(certFile string) (unlock func(), err error) {
	path := LockPath(certFile)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, &Error{Class: ClassWrite, msg: "creating the lock file " + path + " failed", err: err}
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, alreadyInProgress(path)
		}
		return nil, &Error{Class: ClassWrite, msg: "locking " + path + " failed", err: err}
	}
	// unlock() removes path so a stale lock file left by a dead process
	// never wedges every later Lock forever; that unlink races an
	// in-flight Lock here: this fd's flock may hold a now-unlinked inode
	// that a concurrent unlock() already dropped, while a third Lock
	// created and flocked a fresh inode at the same path — two holders,
	// neither aware of the other. Verifying f is still the file actually
	// at path, after the flock succeeded, closes that window: an open fd
	// and a path both name a file by inode, and they must name the same
	// one for the flock to mean anything about path.
	if !sameFileAtPath(f, path) {
		_ = f.Close()
		return nil, alreadyInProgress(path)
	}
	_, _ = fmt.Fprintln(f, strconv.Itoa(os.Getpid()))
	return func() {
		_ = os.Remove(path)
		_ = f.Close()
	}, nil
}

func alreadyInProgress(path string) error {
	return &Error{Class: ClassTemporary, msg: "an enrollment is already in progress on this host (lock file " + path + ")"}
}

// sameFileAtPath reports whether f still names the file currently at path.
func sameFileAtPath(f *os.File, path string) bool {
	fInfo, err := f.Stat()
	if err != nil {
		return false
	}
	pathInfo, err := os.Stat(path)
	if err != nil {
		return false
	}
	return os.SameFile(fInfo, pathInfo)
}
