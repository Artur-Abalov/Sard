// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package retention

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// batchSize is how many ids one restic forget gets: 1000 ids of 64 characters
// are 65 KB of arguments, far below the limit of a command line.
const batchSize = 1000

// relistTimeout bounds the listing after a step was cancelled or timed out.
const relistTimeout = time.Minute

// forgetRun is one FORGET step.
type forgetRun struct {
	store Store
	task  Task
	floor config.Floor
	log   func(level agentv1.LogLevel, text string)
	now   func() time.Time
}

// do lists the snapshots of the source, decides, forgets and reports what is
// gone from the repository (КL9).
func (f *forgetRun) do(ctx context.Context) (*agentv1.StepResult, error) {
	snaps, listedAt, err := f.list(ctx)
	if err != nil {
		return f.unlisted(err)
	}
	d := Plan(snaps, f.task, f.floor, f.now())
	f.logDecision(d)
	if len(d.Forget()) == 0 {
		return f.result(d, snaps, listedAt, nil, nil)
	}
	forgetErr := f.forget(ctx, d)
	after, listedAt, err := f.list(f.afterStep(ctx))
	if err != nil {
		return f.unlisted(errors.Join(forgetErr, err))
	}
	return f.result(d, after, listedAt, d.Forget(), forgetErr)
}

// afterStep is the context to list the repository with after the step: a
// cancelled step still tells what it did.
func (f *forgetRun) afterStep(ctx context.Context) context.Context {
	if ctx.Err() == nil {
		return ctx
	}
	fresh, cancel := context.WithTimeout(context.WithoutCancel(ctx), relistTimeout)
	context.AfterFunc(fresh, cancel)
	return fresh
}

func (f *forgetRun) list(ctx context.Context) ([]restic.Snapshot, time.Time, error) {
	snaps, err := f.store.Snapshots(ctx, []string{SourceTag + f.task.Source})
	return snaps, f.now(), err
}

// forget forgets the parts, then the main snapshots, a batch at a time. A
// step that is cancelled or timed out does not start another batch.
func (f *forgetRun) forget(ctx context.Context, d Decision) error {
	f.log(agentv1.LogLevel_LOG_LEVEL_INFO, "removing stale repository locks only (restic unlock, never --remove-all)")
	if err := f.store.Unlock(ctx); err != nil {
		return err
	}
	if err := f.forgetAll(ctx, d.Parts); err != nil {
		return err
	}
	return f.forgetAll(ctx, d.Mains)
}

func (f *forgetRun) forgetAll(ctx context.Context, ids []string) error {
	for len(ids) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := min(batchSize, len(ids))
		if err := f.store.Forget(ctx, ids[:n]); err != nil {
			return err
		}
		ids = ids[n:]
	}
	return nil
}

// unlisted is the end of a step that could not list the repository: it can
// say neither what is gone nor what remains.
func (f *forgetRun) unlisted(err error) (*agentv1.StepResult, error) {
	failure, holder, err := classify(err)
	out := &agentv1.ForgetOutput{Failure: failure, LockHolder: holder}
	return &agentv1.StepResult{Output: &agentv1.StepResult_Forget{Forget: out}}, err
}

// result reports the repository as it was listed after the step.
func (f *forgetRun) result(d Decision, listed []restic.Snapshot, listedAt time.Time, planned []string, forgetErr error) (*agentv1.StepResult, error) {
	failure, holder, err := classify(forgetErr)
	out := &agentv1.ForgetOutput{
		KeptByFloorSnapshotIds: d.KeptByFloor,
		ListedAt:               timestamppb.New(listedAt),
		Failure:                failure,
		LockHolder:             holder,
	}
	remaining := map[string]bool{}
	for _, s := range listed {
		out.RemainingSnapshotIds = append(out.RemainingSnapshotIds, s.ID)
		remaining[s.ID] = true
	}
	for _, id := range planned {
		if !remaining[id] {
			out.ForgottenSnapshotIds = append(out.ForgottenSnapshotIds, id)
		}
	}
	return &agentv1.StepResult{Output: &agentv1.StepResult_Forget{Forget: out}}, err
}

func (f *forgetRun) logDecision(d Decision) {
	info := func(text string) { f.log(agentv1.LogLevel_LOG_LEVEL_INFO, text) }
	if f.floor.Disabled {
		info("host retention floor is disabled")
	} else {
		info(fmt.Sprintf("host retention floor keep_last=%d keep_within_days=%d", f.floor.KeepLast, f.floor.KeepWithinDays))
	}
	for _, id := range d.KeptByFloor {
		info("kept by host retention floor: " + shortID(id))
	}
	for _, id := range d.Forget() {
		info("forgetting " + shortID(id))
	}
}

// shortID is the id as restic shows it.
func shortID(id string) string { return id[:min(8, len(id))] }
