// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package certpair_test

import "syscall"

func umask(mask int) int { return syscall.Umask(mask) }
