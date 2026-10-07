// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup

import (
	"errors"

	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
)

// LockConfig takes the lock of changes to the config (Р10): a second
// command that changes it is refused at once (CONFIG_LOCKED), the way
// repo init is. The directory of the lock file must exist.
func LockConfig(open repoinit.OpenFunc, l Layout) (unlock func(), f *repoinit.Failure) {
	path := l.LockFile()
	unlock, err := repoinit.Lock(open, path)
	switch {
	case err == nil:
		return unlock, nil
	case errors.Is(err, repoinit.ErrLockHeld):
		return nil, repoinit.Fail(repoinit.ConfigLocked, "another command is changing the agent config right now (lock file %s); wait for it and run the command again", path)
	}
	return nil, repoinit.Fail(repoinit.ConfigWrite, "cannot create the lock file %s: %v", path, err)
}
