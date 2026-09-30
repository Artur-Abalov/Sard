// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoinit

import (
	"errors"
	"github.com/Artur-Abalov/sard/agent/internal/config"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// ErrLockHeld: another init of the repository is running on this host.
var ErrLockHeld = errors.New("repository init is already running")

// LockPath is the lock file of repository name: next to its password file,
// the one directory the operator has made ready for this repository.
func LockPath(passwordFile, name string) string {
	return filepath.Join(filepath.Dir(passwordFile), ".sard-init-"+url.PathEscape(name)+".lock")
}

// Lock takes an exclusive lock on path (В8) and returns the function that
// releases it and removes the file. flock(2) goes away with the process
// however it ends, so the file of a dead process never blocks anyone.
func Lock(path string) (unlock func(), err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
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

// AcquireLock takes the init lock of a repository (В8). Refusals: another
// init is running (INIT_IN_PROGRESS), or the lock file cannot be created
// next to the password file (PASSWORD_FILE_WRITE).
func AcquireLock(repo config.Repository) (func(), *Failure) {
	path := LockPath(repo.PasswordFile, repo.Name)
	unlock, err := Lock(path)
	switch {
	case err == nil:
		return unlock, nil
	case errors.Is(err, ErrLockHeld):
		return nil, fail(InitInProgress, "another init of repository %q is running on this host (lock file %s); wait for it and run the command again if needed", repo.Name, path)
	}
	return nil, fail(PasswordFileWrite, "cannot create the lock file in the directory %s of password_file: %v", filepath.Dir(repo.PasswordFile), err)
}
