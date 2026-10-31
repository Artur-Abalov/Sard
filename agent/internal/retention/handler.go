// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package retention

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Store is the repository as a FORGET step uses it; *restic.CLI is one.
type Store interface {
	// Snapshots lists the snapshots that carry all the tags, without a lock.
	Snapshots(ctx context.Context, tags []string) ([]restic.Snapshot, error)
	// Unlock removes the locks restic considers stale, never a live one.
	Unlock(ctx context.Context) error
	// Forget forgets exactly the snapshots with these ids, waiting for a
	// live lock.
	Forget(ctx context.Context, ids []string) error
}

// Stores opens the repository of a step: its stderr goes to the step's log.
type Stores func(name, commandID string, stderr io.Writer) (Store, bool)

// Options are what a Handler depends on.
type Options struct {
	Stores Stores
	// Floors gives the retention floor of this host for a repository.
	Floors func(repository string) config.Floor
	// Now is the clock of the host.
	Now func() time.Time
}

// Handler carries out the FORGET steps of the server (executor.Handler).
type Handler struct{ opts Options }

// New returns the handler of FORGET steps.
func New(opts Options) *Handler { return &Handler{opts: opts} }

var _ executor.Handler = (*Handler)(nil)

// Actions is FORGET and nothing else.
func (*Handler) Actions() []agentv1.Action {
	return []agentv1.Action{agentv1.Action_ACTION_FORGET}
}

// rejection is a step that is refused before the repository is touched.
type rejection string

func (r rejection) Error() string { return string(r) }

// Is makes the step REJECTED for the executor.
func (rejection) Is(target error) bool { return target == executor.ErrRejected }

// Run implements executor.Handler.
func (h *Handler) Run(ctx context.Context, step *agentv1.RunStep, r executor.Reporter) (*agentv1.StepResult, error) {
	task, err := taskOf(step)
	if err != nil {
		return nil, err
	}
	store, ok := h.opts.Stores(step.GetRepositoryName(), step.GetCommandId(), r.Output())
	if !ok {
		return nil, rejection(fmt.Sprintf("unknown repository %q", step.GetRepositoryName()))
	}
	run := &forgetRun{
		store: store, task: task, log: r.Log, now: h.opts.Now,
		floor: h.opts.Floors(step.GetRepositoryName()),
	}
	return run.do(ctx)
}

// taskOf reads the task of a step and refuses a step with anything outside
// the contract (КL4, КL6, КL7, КL10, КL15): nothing is done before.
func taskOf(step *agentv1.RunStep) (Task, error) {
	if step.GetSnapshotId() != "" {
		return Task{}, rejection("forget takes no snapshot id")
	}
	if outsideContract(step) {
		return Task{}, rejection("forget accepts no overrides")
	}
	f := step.GetForget()
	switch {
	case f == nil:
		return Task{}, rejection("forget needs a task")
	case f.GetSourceId() == "" || strings.Contains(f.GetSourceId(), ","):
		return Task{}, rejection("forget needs a source_id without commas")
	}
	return taskFrom(f)
}

// outsideContract: a plugin, a config, tags, a verify task or a field this
// agent does not know — all of which could bend the floor of the host.
func outsideContract(step *agentv1.RunStep) bool {
	f := step.GetForget()
	return step.GetPlugin() != "" || step.GetConfigJson() != "" || len(step.GetTags()) > 0 || step.GetVerify() != nil ||
		hasUnknown(step) || hasUnknown(f) || hasUnknown(f.GetPolicy())
}

func hasUnknown(m interface{ ProtoReflect() protoreflect.Message }) bool {
	if m == nil || !m.ProtoReflect().IsValid() {
		return false
	}
	return len(m.ProtoReflect().GetUnknown()) > 0
}

func taskFrom(f *agentv1.ForgetTask) (Task, error) {
	p := f.GetPolicy()
	t := Task{
		Source: f.GetSourceId(),
		Policy: Policy{
			Last: p.GetKeepLast(), Daily: p.GetKeepDaily(), Weekly: p.GetKeepWeekly(),
			Monthly: p.GetKeepMonthly(), Yearly: p.GetKeepYearly(), Within: p.GetKeepWithin().AsDuration(),
		},
		Keep: f.GetKeepSnapshotIds(), Incomplete: f.GetIncompleteSnapshotIds(),
		IncompleteMaxAge: f.GetIncompleteMaxAge().AsDuration(),
	}
	switch {
	case t.Policy.Within < 0 || t.IncompleteMaxAge < 0:
		return Task{}, rejection("forget needs durations of zero or more")
	case t.Policy.Empty():
		return Task{}, rejection("retention policy is empty")
	case len(t.Incomplete) > 0 && t.IncompleteMaxAge == 0:
		return Task{}, rejection("incomplete_snapshot_ids need incomplete_max_age")
	}
	return t, nil
}
