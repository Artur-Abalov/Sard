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
	f, err := OpenLockFile(open, path)
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

func sameFile(f *os.File, path string) bool { return SameFileAtPath(f, path) }

// OpenLockFile opens the lock file without following a link and checks it
// is a plain file of its own.
func OpenLockFile(open OpenFunc, path string) (*os.File, error) {
	f, err := open(path, LockFlags, 0o600)
	if err != nil {
		return nil, err
	}
	if err := CheckLockFile(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

// LockFlags open a lock file that may lie in a directory the service user
// writes while the command runs as root: a symbolic link planted at the
// path is refused (O_NOFOLLOW), not written through.
const LockFlags = os.O_CREATE | os.O_RDWR | syscall.O_NOFOLLOW | syscall.O_NOCTTY | syscall.O_CLOEXEC

// ErrUnsafeLockFile: the lock file is not a plain file of its own.
var ErrUnsafeLockFile = errors.New("the lock file is not a regular file with a single name (a link was planted?)")

// CheckLockFile refuses an opened lock file that is not a regular file
// with one name (one that was just removed is the lock's own race, answered
// by SameFileAtPath): a hard link planted to another file would have the lock
// write into it.
func CheckLockFile(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	st, isStat := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || !isStat || st.Nlink > 1 {
		return ErrUnsafeLockFile
	}
	return nil
}

// SameFileAtPath says whether f is still the file at path, looked at
// without following a link.
func SameFileAtPath(f *os.File, path string) bool {
	held, err := f.Stat()
	if err != nil {
		return false
	}
	current, err := os.Lstat(path)
	return err == nil && os.SameFile(held, current)
}
