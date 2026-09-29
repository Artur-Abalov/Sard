// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package enroll_test

import "syscall"

// setUmask sets the process umask and returns the previous one, for tests
// that check a file's mode does not depend on it.
func setUmask(mask int) int {
	return syscall.Umask(mask)
}
