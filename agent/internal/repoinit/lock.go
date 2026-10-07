// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoinit

import (
	"errors"
	"net/url"
	"path/filepath"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
)

// ErrLockHeld, OpenFunc and Lock are the refusal package's.
var ErrLockHeld = refusal.ErrLockHeld

// OpenFunc is os.OpenFile; see refusal.OpenFunc.
type OpenFunc = refusal.OpenFunc

// Lock takes an exclusive lock on path; see refusal.Lock.
func Lock(open OpenFunc, path string) (unlock func(), err error) { return refusal.Lock(open, path) }

// LockPath is the lock file of repository name: in restic.cache_dir, the
// directory the service user owns and the package prepares (В8а, ADR 0030).
func LockPath(cacheDir, name string) string {
	return filepath.Join(cacheDir, ".sard-init-"+url.PathEscape(name)+".lock")
}

// AcquireLock takes the init lock of a repository (В8) in cacheDir, which
// is never created here. Refusals: another init is running
// (INIT_IN_PROGRESS), or the lock file cannot be created (LOCK_WRITE).
func AcquireLock(open OpenFunc, cacheDir string, repo config.Repository) (func(), *refusal.Failure) {
	path := LockPath(cacheDir, repo.Name)
	unlock, err := Lock(open, path)
	switch {
	case err == nil:
		return unlock, nil
	case errors.Is(err, ErrLockHeld):
		return nil, refusal.Fail(refusal.InitInProgress, "another init of repository %q is running on this host (lock file %s); wait for it and run the command again if needed", repo.Name, path)
	}
	return nil, refusal.Fail(refusal.LockWrite, "cannot create the lock file %s in restic.cache_dir %s: %v; the directory must exist and be writable by the user running this command", path, cacheDir, err)
}
