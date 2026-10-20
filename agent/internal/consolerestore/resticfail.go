// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore

import (
	"context"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restore"
)

// resticFailure tells a failure of restic while it restored as the other
// commands do (ВК7): what it could not write to dir, then the reasons of
// repoinit.FromRestic, which also words a stopped step. Nothing stays in dir,
// so the text does not speak of partly restored files.
func resticFailure(ctx context.Context, err error, stage Stage, dir string, target repoinit.Target) *Failure {
	if ctx.Err() == nil {
		if f := restore.TargetFailure(err, dir, target, false); f != nil {
			return asFailure(f, stage, target)
		}
	}
	return asFailure(repoinit.FromRestic(ctx, err, target), stage, target)
}

// asFailure puts a refusal of the shared code at a stage of this restore. The
// Failure says "nothing was restored" itself.
func asFailure(f *refusal.Failure, stage Stage, target repoinit.Target) *Failure {
	reason := f.Reason
	if reason == refusal.BackendRefused && !target.Remote {
		// A local repository has no storage to refuse: this is restic failing
		// in a way the agent has no name for.
		reason = refusal.ResticFailed
	}
	return &Failure{Reason: reason, Stage: stage, Detail: strings.TrimSuffix(f.Detail, "; nothing was restored")}
}
