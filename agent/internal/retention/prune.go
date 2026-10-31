// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package retention

import (
	"context"
	"fmt"
	"io"

	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// PruneStore is the repository as a PRUNE step uses it; *restic.CLI is one.
type PruneStore interface {
	// Unlock removes the locks restic considers stale, never a live one.
	Unlock(ctx context.Context) error
	// Prune removes the data no snapshot refers to, waiting for a live lock.
	Prune(ctx context.Context) (restic.PruneSummary, error)
}

// PruneStores opens the repository of a step: its stderr goes to the step's log.
type PruneStores func(name, commandID string, stderr io.Writer) (PruneStore, bool)

// PruneOptions are what a PruneHandler depends on.
type PruneOptions struct {
	Stores PruneStores
}

// PruneHandler carries out the PRUNE steps of the server (executor.Handler).
type PruneHandler struct{ opts PruneOptions }

// NewPrune returns the handler of PRUNE steps.
func NewPrune(opts PruneOptions) *PruneHandler { return &PruneHandler{opts: opts} }

var _ executor.Handler = (*PruneHandler)(nil)

// Actions is PRUNE and nothing else.
func (*PruneHandler) Actions() []agentv1.Action {
	return []agentv1.Action{agentv1.Action_ACTION_PRUNE}
}

// Run implements executor.Handler: restic unlock without --remove-all, then
// restic prune. It forgets no snapshot (КL16).
func (h *PruneHandler) Run(ctx context.Context, step *agentv1.RunStep, r executor.Reporter) (*agentv1.StepResult, error) {
	if err := checkPrune(step); err != nil {
		return nil, err
	}
	store, ok := h.opts.Stores(step.GetRepositoryName(), step.GetCommandId(), r.Output())
	if !ok {
		return nil, rejection(fmt.Sprintf("unknown repository %q", step.GetRepositoryName()))
	}
	return runPrune(ctx, store, r)
}

// runPrune unlocks, prunes and reports.
func runPrune(ctx context.Context, store PruneStore, r executor.Reporter) (*agentv1.StepResult, error) {
	r.Log(agentv1.LogLevel_LOG_LEVEL_INFO, "removing stale repository locks only (restic unlock, never --remove-all)")
	if err := store.Unlock(ctx); err != nil {
		return pruneFailed(err)
	}
	sum, err := store.Prune(ctx)
	if err != nil {
		return pruneFailed(err)
	}
	logSummary(r, sum)
	out := &agentv1.PruneOutput{FreedBytes: sum.Freed, RepositorySizeBytes: sum.Size}
	return &agentv1.StepResult{Output: &agentv1.StepResult_Prune{Prune: out}}, nil
}

// logSummary puts the summary of restic prune into the log of the step and
// names the freed space when both numbers are known.
func logSummary(r executor.Reporter, sum restic.PruneSummary) {
	for _, line := range sum.Lines {
		r.Log(agentv1.LogLevel_LOG_LEVEL_INFO, line)
	}
	if sum.Freed != nil && sum.Size != nil {
		r.Log(agentv1.LogLevel_LOG_LEVEL_INFO, fmt.Sprintf("prune freed %d bytes, repository size %d bytes", *sum.Freed, *sum.Size))
	}
}

// checkPrune refuses a step with anything outside the contract (КПр2): a
// plugin, a config, tags, a snapshot, another task or a field this agent
// does not know. Nothing is done before.
func checkPrune(step *agentv1.RunStep) error {
	switch {
	case beyondPrune(step):
		return rejection("prune accepts no overrides")
	case step.GetPrune() == nil:
		return rejection("prune task missing")
	}
	return nil
}

// beyondPrune: the step carries something PRUNE does not use.
func beyondPrune(step *agentv1.RunStep) bool {
	return hasOtherFields(step) || step.GetSnapshotId() != "" || step.GetForget() != nil ||
		hasUnknown(step) || hasUnknown(step.GetPrune())
}

// pruneFailed is the end of a step whose restic call failed (КПр5).
func pruneFailed(err error) (*agentv1.StepResult, error) {
	failure, holder, err := classify(err)
	out := &agentv1.PruneOutput{Failure: failure, LockHolder: holder}
	return &agentv1.StepResult{Output: &agentv1.StepResult_Prune{Prune: out}}, err
}
