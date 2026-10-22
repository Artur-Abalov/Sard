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

// Classifier words the failures of restic for the plugins that restore
// themselves (R2c): as the console words them, the stage left to the plugin.
// The repositories are described by target (secrets hidden); nil: just by
// their names.
type Classifier struct {
	target func(repository string) repoinit.Target
}

// NewClassifier returns a Classifier of the repositories target describes.
func NewClassifier(target func(repository string) repoinit.Target) *Classifier {
	return &Classifier{target: target}
}

func (c *Classifier) targetOf(name string) repoinit.Target {
	if c.target != nil {
		return c.target(name)
	}
	return repoinit.Named(name)
}

// Snapshots words a failure of restic while a plugin listed the snapshots of
// the repository.
func (c *Classifier) Snapshots(ctx context.Context, repository string, err error) *Failure {
	target := c.targetOf(repository)
	return asFailure(restore.ListFailure(ctx, err, target), "", target)
}

// Dump words a failure of restic while a plugin read a file of a snapshot.
func (c *Classifier) Dump(ctx context.Context, repository string, err error) *Failure {
	target := c.targetOf(repository)
	return asFailure(repoinit.FromRestic(ctx, err, target), "", target)
}
