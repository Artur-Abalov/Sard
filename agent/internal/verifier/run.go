// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package verifier

import (
	"context"
	"errors"
	"fmt"
	"path"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// outputLimit is the most of a stage's output that is kept: its tail.
const outputLimit = 4096

// errOutOfTime ends a stage that ran past its limit.
var errOutOfTime = errors.New("the stage ran out of its time limit")

// outcome is how a stage ended.
type outcome struct {
	status   agentv1.StageStatus
	output   string
	exitCode int32
	// timedOut: the stage ran past its limit; the verification did not take place.
	timedOut bool
	// digest is the image of the sandbox the stage started.
	digest string
}

func passed(format string, args ...any) outcome {
	return outcome{status: agentv1.StageStatus_STAGE_STATUS_PASSED, output: fmt.Sprintf(format, args...)}
}

func failed(format string, args ...any) outcome {
	return outcome{status: agentv1.StageStatus_STAGE_STATUS_FAILED, output: fmt.Sprintf(format, args...)}
}

func cannotRun(format string, args ...any) outcome {
	return outcome{status: agentv1.StageStatus_STAGE_STATUS_CANNOT_RUN, output: fmt.Sprintf(format, args...)}
}

// proves: the stage passed or does not apply, and the pipeline goes on.
func (o outcome) proves() bool {
	return o.status == agentv1.StageStatus_STAGE_STATUS_PASSED || o.status == agentv1.StageStatus_STAGE_STATUS_NOT_APPLICABLE
}

// stage is one stage of the pipeline.
type stage struct {
	level uint32
	name  string
	limit time.Duration
	run   func(ctx context.Context) outcome
}

// run is one verification.
type run struct {
	ctx    context.Context
	opts   Options
	step   *agentv1.RunStep
	plugin Plugin
	store  Store
	r      executor.Reporter
	dir    string
	out    *agentv1.VerifyOutput
	// stopped is the first stage that did not pass, nil while all did.
	stopped   *agentv1.VerifyStage
	stopping  outcome
	ids       []string
	listed    []restic.Snapshot
	reference *sdk.Fingerprint
	copied    sdk.RestoredCopy
	restore   time.Duration
	// box is the sandbox of a sandboxed plugin, password its application's.
	box      Sandbox
	password string
	// digest is the image of the sandbox, in the evidence of every later stage.
	digest string
}

func newRun(ctx context.Context, v *Verifier, step *agentv1.RunStep, p Plugin, store Store, r executor.Reporter) *run {
	return &run{
		ctx: ctx, opts: v.opts, step: step, plugin: p, store: store, r: r,
		dir: path.Join(v.opts.WorkDir, step.GetVerify().GetJobId()),
		out: &agentv1.VerifyOutput{SnapshotId: step.GetSnapshotId()},
	}
}

func (x *run) pipeline() []stage {
	l := x.opts.Limits
	stages := []stage{
		{0, "snapshot", l.Prepare, x.snapshot},
		{0, "fingerprint_tag", l.Prepare, x.fingerprintTag},
		{0, "work_volume", l.Prepare, x.workVolume},
		{1, "restic_check", l.Check, x.check},
	}
	if _, ok := x.sandboxed(); ok {
		// Pulling an image takes the time of a restore, not of a preparation.
		stages = append(stages, stage{0, "sandbox", l.Restore, x.startSandbox})
	}
	return append(stages,
		stage{2, "restore", l.Restore, x.restoreCopy},
		stage{3, "start", l.Restore, x.start},
		stage{4, "fingerprint", l.Fingerprint, x.fingerprint},
	)
}

// execute runs the stages up to the first that does not pass, then the
// cleanup, whatever happened.
func (x *run) execute() (*agentv1.StepResult, error) {
	for _, s := range x.pipeline() {
		if !x.stage(s) {
			break
		}
	}
	x.stage(stage{0, "cleanup", x.opts.Limits.Prepare, x.cleanup})
	return &agentv1.StepResult{Output: &agentv1.StepResult_Verify{Verify: x.out}}, x.verdict()
}

// stage runs s within its limit, records and reports it, and says whether
// the pipeline goes on.
func (x *run) stage(s stage) bool {
	started := x.opts.Clock.Now()
	limit := x.limit(s.limit, started)
	ctx, cancel := context.WithCancelCause(x.ctx)
	timer := x.opts.Clock.AfterFunc(limit, func() { cancel(errOutOfTime) })
	o := s.run(ctx)
	timer.Stop()
	if context.Cause(ctx) == errOutOfTime {
		o = outcome{status: agentv1.StageStatus_STAGE_STATUS_CANNOT_RUN, output: fmt.Sprintf("%v of %v", errOutOfTime, limit), timedOut: true}
	}
	cancel(nil)
	finished := x.opts.Clock.Now()
	if o.digest != "" {
		x.digest = o.digest
	}
	vs := &agentv1.VerifyStage{
		Level: s.level, Name: s.name, Status: o.status, ExitCode: o.exitCode, Output: tail(o.output),
		StartedAt: timestamppb.New(started), FinishedAt: timestamppb.New(finished), ImageDigest: x.digest,
	}
	x.out.Stages = append(x.out.Stages, vs)
	x.r.Stage(vs)
	x.timeRestore(s, o, finished.Sub(started))
	return x.record(vs, o)
}

// limit is the stage's own limit, or the time the job has left if that is shorter.
func (x *run) limit(own time.Duration, now time.Time) time.Duration {
	return min(own, max(x.step.GetVerify().GetDeadline().AsTime().Sub(now), 0))
}

// timeRestore adds the stages that bring the copy up to the restore time (level 5).
func (x *run) timeRestore(s stage, o outcome, took time.Duration) {
	if s.level != 2 && s.level != 3 || !o.proves() {
		return
	}
	x.restore += took
	x.out.RestoreDuration = durationpb.New(x.restore)
}

// record keeps the first stage that did not pass; true while all did.
func (x *run) record(vs *agentv1.VerifyStage, o outcome) bool {
	if o.proves() {
		return true
	}
	if x.stopped == nil {
		x.stopped, x.stopping = vs, o
	}
	return false
}

// verdict is the verdict of the stages, set on the output, and the error the
// step fails with; nil when everything passed.
func (x *run) verdict() error {
	x.out.Verdict = agentv1.VerifyVerdict_VERIFY_VERDICT_PASSED
	if x.stopped == nil {
		return nil
	}
	switch {
	case x.stopping.timedOut:
		x.out.Verdict = agentv1.VerifyVerdict_VERIFY_VERDICT_INFRASTRUCTURE_FAILURE
	case x.stopping.status == agentv1.StageStatus_STAGE_STATUS_FAILED:
		x.out.Verdict = agentv1.VerifyVerdict_VERIFY_VERDICT_FAILED
	default:
		x.out.Verdict = agentv1.VerifyVerdict_VERIFY_VERDICT_CANNOT_VERIFY
	}
	return fmt.Errorf("stage %s: %s", x.stopped.GetName(), x.stopped.GetOutput())
}

// tail is the end of a long output.
func tail(s string) string {
	if len(s) <= outputLimit {
		return s
	}
	return "…" + s[len(s)-outputLimit:]
}
