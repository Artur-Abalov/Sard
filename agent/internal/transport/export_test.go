// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package transport

import (
	"google.golang.org/grpc/keepalive"
)

// Internals exposed to the external tests of this package.

func KeepaliveForTest() keepalive.ClientParameters { return keepaliveParams() }

func (t *Transport) UsesRealClockForTest() bool {
	_, ok := t.opts.Clock.(realClock)
	return ok
}
