// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoinit

import (
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// FromCheck explains a restic binary the agent cannot use (С7, С8).
// pathConfigured says whether restic.path of the config named the binary
// or it is the restic next to sard-agent.
func FromCheck(ce *restic.CheckError, pathConfigured bool, configPath string) *Failure {
	switch ce.Problem {
	case restic.NotFound:
		return notFound(ce, pathConfigured, configPath)
	case restic.TooOld:
		return fail(ResticTooOld, "restic %s at %s is older than the minimum %s; install restic %s or newer (the release shipped with this agent is %s) or point restic.path in %s at one",
			ce.Found, ce.Binary, ce.Minimum, ce.Minimum, restic.Pinned, configPath)
	}
	return fail(ResticUnusable, "%s cannot be used as restic: %v", ce.Binary, ce.Err)
}

func notFound(ce *restic.CheckError, pathConfigured bool, configPath string) *Failure {
	if pathConfigured {
		return fail(ResticNotFound, "restic was not found at %s (restic.path in %s); install restic %s or newer there, or point restic.path at an existing restic binary",
			ce.Binary, configPath, restic.Minimum)
	}
	return fail(ResticNotFound, "restic was not found at %s, next to the sard-agent binary: restic.path is not set, it can be set in the agent config %s; the minimum restic version is %s",
		ce.Binary, configPath, restic.Minimum)
}
