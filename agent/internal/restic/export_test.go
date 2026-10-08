// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic

import "time"

// Internals exposed to the external tests of this package.
var (
	SignalGroup   = signalGroup
	MustVersions  = mustVersions
	CredentialFor = credentialFor
)

func (p ProcessExecutor) GraceForTest() time.Duration { return p.grace() }
