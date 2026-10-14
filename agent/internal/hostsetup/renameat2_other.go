// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:build linux && !amd64 && !arm64

package hostsetup

// sysRenameat2 is a number no system call has: the agent is built for amd64
// and arm64, and on any other architecture the call fails with ENOSYS.
const sysRenameat2 = 1<<31 - 1
