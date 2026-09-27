// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package executor_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/executor"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
	"google.golang.org/protobuf/types/known/durationpb"
)

const (
	succeeded = agentv1.StepStatus_STEP_STATUS_SUCCEEDED
	failed    = agentv1.StepStatus_STEP_STATUS_FAILED
	cancelled = agentv1.StepStatus_STEP_STATUS_CANCELLED
	timedOut  = agentv1.StepStatus_STEP_STATUS_TIMED_OUT
	rejected  = agentv1.StepStatus_STEP_STATUS_REJECTED
	dumping   = agentv1.StepPhase_STEP_PHASE_DUMPING
	uploading = agentv1.StepPhase_STEP_PHASE_UPLOADING
)

func wantResult(t *testing.T, r *agentv1.StepResult, id string, status agentv1.StepStatus, message string) {
	t.Helper()
	if r.GetCommandId() != id || r.GetStatus() != status || r.GetMessage() != message {
		t.Fatalf("result = %s %v %q, want %s %v %q", r.GetCommandId(), r.GetStatus(), r.GetMessage(), id, status, message)
	}
}

func wantProgress(t *testing.T, p *agentv1.StepProgress, id string, phase agentv1.StepPhase, processed uint64) {
	t.Helper()
	if p.GetCommandId() != id || p.GetPhase() != phase || p.GetBytesProcessed() != processed {
		t.Fatalf("progress = %v, want %s %v %d", p, id, phase, processed)
	}
}

// --- 1. deduplication

func TestADuplicateOfARunningCommandRepeatsItsProgressAndDoesNotRunAgain(t *testing.T) {
	f := setup(t, nil)
	f.e.Submit(backup("c1"))
	accepted(t, f.sink, "c1")
	c := f.files.next(t)
	c.r.Progress(dumping, 10, 100)
	wantProgress(t, f.sink.progress(t), "c1", dumping, 10)

	f.e.Submit(backup("c1"))
	wantProgress(t, f.sink.progress(t), "c1", dumping, 10)
	f.files.idle(t)

	c.finish(snapshot("s1"), nil)
	first := f.sink.result(t)
	wantResult(t, first, "c1", succeeded, "")

	f.e.Submit(backup("c1"))
	again := f.sink.result(t)
	if again.GetBackup().GetSnapshotId() != "s1" || again.GetFinishedAt().AsTime() != first.GetFinishedAt().AsTime() {
		t.Fatalf("duplicate after finish = %v, want %v", again, first)
	}
	f.files.idle(t)
	if n := f.files.runs.Load(); n != 1 {
		t.Fatalf("handler ran %d times", n)
	}
}

func TestADuplicateOfAQueuedCommandRepeatsAccepted(t *testing.T) {
	f := setup(t, nil)
	f.e.Submit(backup("c1"))
	accepted(t, f.sink, "c1")
	f.files.next(t)
	f.e.Submit(backup("c2"))
	accepted(t, f.sink, "c2")
	f.e.Submit(backup("c2"))
	accepted(t, f.sink, "c2")
	f.sink.quiet(t)
}

// --- 3. cancellation in every state

func TestCancellingAQueuedCommandNeverStartsIt(t *testing.T) {
	f := setup(t, nil)
	f.e.Submit(backup("c1"))
	accepted(t, f.sink, "c1")
	first := f.files.next(t)
	f.e.Submit(backup("c2"))
	accepted(t, f.sink, "c2")

	f.e.Cancel("c2")
	r := f.sink.result(t)
	wantResult(t, r, "c2", cancelled, "cancelled before it started")
	if r.GetStartedAt() != nil || r.GetFinishedAt() == nil {
		t.Fatalf("times = %v %v", r.GetStartedAt(), r.GetFinishedAt())
	}
	first.finish(snapshot("s1"), nil)
	wantResult(t, f.sink.result(t), "c1", succeeded, "")
	f.files.idle(t)
}

func TestCancellingARunningCommandCancelsItsContext(t *testing.T) {
	f := setup(t, nil)
	f.e.Submit(backup("c1"))
	accepted(t, f.sink, "c1")
	c := f.files.next(t)
	f.e.Cancel("c1")
	<-c.ctx.Done()
	r := f.sink.result(t)
	wantResult(t, r, "c1", cancelled, "cancelled by the server")
	if r.GetStartedAt() == nil {
		t.Fatal("a started step reports started_at")
	}
}

func TestCancellingAFinishedCommandReturnsItsRealResult(t *testing.T) {
	f := setup(t, nil)
	f.e.Submit(backup("c1"))
	accepted(t, f.sink, "c1")
	f.files.next(t).finish(snapshot("s1"), nil)
	f.sink.result(t)
	f.e.Cancel("c1")
	r := f.sink.result(t)
	wantResult(t, r, "c1", succeeded, "")
	if r.GetBackup().GetSnapshotId() != "s1" {
		t.Fatalf("output = %v", r.GetOutput())
	}
}

func TestCancellingAnUnknownCommandIsIgnored(t *testing.T) {
	f := setup(t, nil)
	f.e.Cancel("nope")
	f.sink.quiet(t)
	if !strings.Contains(f.log.String(), "nope") {
		t.Fatalf("log = %s", f.log)
	}
}

func TestAHandlerThatIgnoresCancelIsGivenUpAfterFourDoublingChecks(t *testing.T) {
	f := setup(t, nil)
	s := backup("c1")
	s.Plugin = "stubborn"
	f.e.Submit(s)
	accepted(t, f.sink, "c1")
	c := f.stubborn.next(t)
	f.e.Submit(backup("c2"))
	accepted(t, f.sink, "c2")
	f.e.Cancel("c1")

	f.clock.Advance(2*time.Second + 4*time.Second + 8*time.Second + 16*time.Second - time.Nanosecond)
	f.sink.quiet(t)
	f.clock.Advance(time.Nanosecond)
	wantResult(t, f.sink.result(t), "c1", cancelled, "cancelled by the server; the plugin did not stop within 30s")
	if got := strings.Count(f.log.String(), "plugin has not stopped"); got != 4 {
		t.Fatalf("%d warnings; log:\n%s", got, f.log)
	}
	if !strings.Contains(f.log.String(), "giving up on a plugin") {
		t.Fatalf("log:\n%s", f.log)
	}
	f.files.next(t)                     // the slot is free again
	if n := f.clock.pending(); n != 1 { // only c2's timeout
		t.Fatalf("%d timers armed", n)
	}

	c.finish(snapshot("late"), nil) // a late return changes nothing
	f.sink.quiet(t)
	if !strings.Contains(f.log.String(), "late return of a plugin ignored") {
		t.Fatalf("log:\n%s", f.log)
	}
}

func TestATimerThatFiresAfterTheStepFinishedChangesNothing(t *testing.T) {
	f := setup(t, nil)
	f.clock.late = true
	s := backup("c1")
	s.Timeout = durationpb.New(time.Second)
	f.e.Submit(s)
	accepted(t, f.sink, "c1")
	c := f.files.next(t)
	f.e.Cancel("c1") // arms the recheck timer
	<-c.ctx.Done()
	wantResult(t, f.sink.result(t), "c1", cancelled, "cancelled by the server")
	f.clock.Advance(time.Minute) // step timeout and all rechecks fire now
	f.sink.quiet(t)
	if strings.Contains(f.log.String(), "plugin has not stopped") {
		t.Fatalf("a finished step was rechecked; log:\n%s", f.log)
	}
}

// --- 4. timeout

func TestAStepTimesOutAfterItsOwnTimeout(t *testing.T) {
	f := setup(t, nil)
	s := backup("c1")
	s.Timeout = durationpb.New(5 * time.Second)
	f.e.Submit(s)
	accepted(t, f.sink, "c1")
	c := f.files.next(t)
	f.clock.Advance(5*time.Second - time.Nanosecond)
	f.sink.quiet(t)
	f.clock.Advance(time.Nanosecond)
	<-c.ctx.Done()
	wantResult(t, f.sink.result(t), "c1", timedOut, "exceeded the step timeout of 5s")
}

func TestAStepWithoutTimeoutGetsTheMaximum(t *testing.T) {
	f := setup(t, func(o *executor.Options) { o.MaxTimeout = time.Hour })
	f.e.Submit(backup("c1"))
	accepted(t, f.sink, "c1")
	f.files.next(t)
	f.clock.Advance(time.Hour - time.Nanosecond)
	f.sink.quiet(t)
	f.clock.Advance(time.Nanosecond)
	wantResult(t, f.sink.result(t), "c1", timedOut, "exceeded the agent's maximum step timeout of 1h0m0s")
}

func TestAStepFinishingJustBeforeItsTimeoutSucceeds(t *testing.T) {
	f := setup(t, nil)
	s := backup("c1")
	s.Timeout = durationpb.New(time.Minute)
	f.e.Submit(s)
	accepted(t, f.sink, "c1")
	f.files.next(t).finish(snapshot("s1"), nil)
	wantResult(t, f.sink.result(t), "c1", succeeded, "")
	if n := f.clock.pending(); n != 0 {
		t.Fatalf("%d timers still armed after the step finished", n)
	}
	f.clock.Advance(time.Hour)
	f.sink.quiet(t)
}

func TestAZeroTimeoutMeansTheMaximumAndOneNanosecondIsHonoured(t *testing.T) {
	f := setup(t, func(o *executor.Options) { o.MaxTimeout = time.Minute })
	zero, tiny := backup("zero"), backup("tiny")
	zero.Timeout, tiny.Timeout = durationpb.New(0), durationpb.New(time.Nanosecond)
	f.e.Submit(zero)
	accepted(t, f.sink, "zero")
	f.files.next(t)
	f.clock.Advance(time.Minute)
	wantResult(t, f.sink.result(t), "zero", timedOut, "exceeded the agent's maximum step timeout of 1m0s")
	f.e.Submit(tiny)
	accepted(t, f.sink, "tiny")
	f.files.next(t)
	f.clock.Advance(time.Nanosecond)
	wantResult(t, f.sink.result(t), "tiny", timedOut, "exceeded the step timeout of 1ns")
}

// --- 5. panic

func TestAPanickingHandlerFailsTheStepAndTheNextStepRuns(t *testing.T) {
	f := setup(t, nil)
	f.e.Submit(backup("c1"))
	accepted(t, f.sink, "c1")
	f.e.Submit(backup("c2"))
	accepted(t, f.sink, "c2")
	f.files.next(t).panic("boom")
	wantResult(t, f.sink.result(t), "c1", failed, "plugin panicked: boom")
	if log := f.log.String(); !strings.Contains(log, "plugin panicked") || !strings.Contains(log, "goroutine") {
		t.Fatalf("the panic and its stack are logged; log:\n%s", log)
	}
	f.files.next(t).finish(snapshot("s2"), nil)
	wantResult(t, f.sink.result(t), "c2", succeeded, "")
}

// --- 6. REJECTED

func TestStepsThatCannotRunAreRejectedWithoutStartingTheHandler(t *testing.T) {
	cases := map[string]struct {
		edit    func(*agentv1.RunStep)
		message string
	}{
		"unknown plugin": {func(s *agentv1.RunStep) { s.Plugin = "tape" }, `unknown plugin "tape"`},
		"unsupported action": {func(s *agentv1.RunStep) { s.Action = agentv1.Action_ACTION_RESTORE },
			`plugin "files" does not support ACTION_RESTORE`},
		"unspecified action": {func(s *agentv1.RunStep) { s.Action = agentv1.Action_ACTION_UNSPECIFIED },
			`plugin "files" does not support ACTION_UNSPECIFIED`},
		"unknown repository": {func(s *agentv1.RunStep) { s.RepositoryName = "offsite" }, `unknown repository "offsite"`},
		"missing repository": {func(s *agentv1.RunStep) { s.RepositoryName = "" }, `ACTION_BACKUP needs a repository`},
		"script with an unknown repository": {func(s *agentv1.RunStep) {
			s.Action, s.RepositoryName = agentv1.Action_ACTION_RUN, "offsite"
		}, `unknown repository "offsite"`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := setup(t, nil)
			s := backup("c1")
			tc.edit(s)
			f.e.Submit(s)
			r := f.sink.result(t)
			wantResult(t, r, "c1", rejected, tc.message)
			if r.GetStartedAt() != nil {
				t.Fatal("a rejected step never started")
			}
			f.e.Submit(s)
			wantResult(t, f.sink.result(t), "c1", rejected, tc.message)
			f.files.idle(t)
		})
	}
}

func TestUnspecifiedActionIsRejectedEvenIfAPluginListsIt(t *testing.T) {
	f := setup(t, nil)
	s := backup("c1")
	s.Plugin, s.Action = "sloppy", agentv1.Action_ACTION_UNSPECIFIED
	f.e.Submit(s)
	wantResult(t, f.sink.result(t), "c1", rejected, `plugin "sloppy" does not support ACTION_UNSPECIFIED`)
}

func TestUnspecifiedProgressFromAHandlerIsDropped(t *testing.T) {
	f := setup(t, nil)
	f.e.Submit(backup("c1"))
	accepted(t, f.sink, "c1")
	c := f.files.next(t)
	f.clock.Advance(time.Minute)
	c.r.Progress(agentv1.StepPhase_STEP_PHASE_UNSPECIFIED, 1, 2)
	f.sink.quiet(t)
}

func TestAScriptWithoutRepositoryIsAccepted(t *testing.T) {
	f := setup(t, nil)
	s := backup("c1")
	s.Action, s.RepositoryName = agentv1.Action_ACTION_RUN, ""
	f.e.Submit(s)
	accepted(t, f.sink, "c1")
	f.files.next(t)
}

func TestAValidationErrorFromTheHandlerIsRejected(t *testing.T) {
	f := setup(t, nil)
	f.e.Submit(backup("c1"))
	accepted(t, f.sink, "c1")
	f.files.next(t).finish(nil, fmt.Errorf("%w: unknown secret %q", executor.ErrRejected, "db"))
	wantResult(t, f.sink.result(t), "c1", rejected, `step rejected: unknown secret "db"`)
}

func TestAnOverflowingQueueRejects(t *testing.T) {
	f := setup(t, func(o *executor.Options) { o.MaxQueue = 1 })
	f.e.Submit(backup("c1"))
	accepted(t, f.sink, "c1")
	f.files.next(t)
	f.e.Submit(backup("c2"))
	accepted(t, f.sink, "c2")
	f.e.Submit(backup("c3"))
	wantResult(t, f.sink.result(t), "c3", rejected, "the queue is full (1 steps waiting)")
}

func TestAHandlerErrorFailsTheStepAndKeepsItsOutput(t *testing.T) {
	f := setup(t, nil)
	f.e.Submit(backup("c1"))
	accepted(t, f.sink, "c1")
	f.files.next(t).finish(snapshot("partial"), errors.New("restic: exit 3"))
	r := f.sink.result(t)
	wantResult(t, r, "c1", failed, "restic: exit 3")
	if r.GetBackup().GetSnapshotId() != "partial" {
		t.Fatalf("output = %v", r.GetOutput())
	}
}

func TestACommandWithoutIDIsIgnored(t *testing.T) {
	f := setup(t, nil)
	f.e.Submit(backup(""))
	f.sink.quiet(t)
	f.files.idle(t)
	if !strings.Contains(f.log.String(), "step without command_id ignored") {
		t.Fatalf("log:\n%s", f.log)
	}
}

// --- 7. concurrency

// arrivals submits total steps from concurrent callers and returns the order
// the executor accepted them in.
func arrivals(t *testing.T, f *fixture, total int) []string {
	t.Helper()
	for i := range total {
		go f.e.Submit(backup(fmt.Sprintf("c%02d", i)))
	}
	var arrived []string
	for len(arrived) < total {
		if p := f.sink.progress(t); p.GetPhase() == agentv1.StepPhase_STEP_PHASE_ACCEPTED {
			arrived = append(arrived, p.GetCommandId())
		}
	}
	return arrived
}

// firstWave collects the steps that start together and checks they are the first arrivals.
func firstWave(t *testing.T, f *fixture, arrived []string) map[string]*call {
	t.Helper()
	running := map[string]*call{}
	for range arrived {
		c := f.files.next(t)
		running[c.step.GetCommandId()] = c
	}
	for _, id := range arrived {
		if running[id] == nil {
			t.Fatalf("first wave %v, first arrivals %v", running, arrived)
		}
	}
	f.files.idle(t)
	return running
}

func TestAtMostNStepsRunAtOnceAndTheyStartInArrivalOrder(t *testing.T) {
	const parallel, total = 3, 12
	f := setup(t, func(o *executor.Options) { o.MaxParallel = parallel })
	arrived := arrivals(t, f, total)
	running := firstWave(t, f, arrived[:parallel])
	// Each freed slot then goes to the next arrival, one at a time.
	for _, id := range arrived[parallel:] {
		for done, c := range running {
			c.finish(snapshot("s"), nil)
			delete(running, done)
			break
		}
		c := f.files.next(t)
		if c.step.GetCommandId() != id {
			t.Fatalf("started %s, next arrival is %s", c.step.GetCommandId(), id)
		}
		running[id] = c
	}
	if peak := f.files.peak.Load(); peak > parallel {
		t.Fatalf("%d steps ran at once, limit %d", peak, parallel)
	}
}

// --- 8. progress rate

func TestProgressIsLimitedToOnePerIntervalExceptPhaseChanges(t *testing.T) {
	f := setup(t, nil)
	f.e.Submit(backup("c1"))
	accepted(t, f.sink, "c1")
	c := f.files.next(t)

	c.r.Progress(dumping, 1, 100)
	c.r.Progress(dumping, 2, 100)
	c.r.Progress(dumping, 3, 100)
	if p := f.sink.progress(t); p.GetBytesProcessed() != 1 {
		t.Fatalf("progress = %v", p)
	}
	c.r.Progress(uploading, 4, 100) // a new phase always passes
	if p := f.sink.progress(t); p.GetPhase() != uploading || p.GetBytesProcessed() != 4 {
		t.Fatalf("progress = %v", p)
	}
	f.clock.Advance(time.Second - time.Nanosecond)
	c.r.Progress(uploading, 5, 100)
	f.sink.quiet(t)
	f.clock.Advance(time.Nanosecond)
	c.r.Progress(uploading, 6, 100)
	if p := f.sink.progress(t); p.GetBytesProcessed() != 6 || p.GetSentAt() == nil {
		t.Fatalf("progress = %v", p)
	}
	c.r.Progress(agentv1.StepPhase_STEP_PHASE_ACCEPTED, 7, 100) // only the executor accepts
	f.sink.quiet(t)
}

func TestLogLinesOfARunningStepReachTheSink(t *testing.T) {
	f := setup(t, nil)
	f.e.Submit(backup("c1"))
	accepted(t, f.sink, "c1")
	c := f.files.next(t)
	c.r.Log(agentv1.LogLevel_LOG_LEVEL_INFO, "dumping db")
	e := f.sink.next(t)
	if e.logID != "c1" || e.log.GetText() != "dumping db" || e.log.GetLevel() != agentv1.LogLevel_LOG_LEVEL_INFO || e.log.GetTime() == nil {
		t.Fatalf("log = %+v", e)
	}
	c.finish(snapshot("s1"), nil)
	f.sink.result(t)
	c.r.Log(agentv1.LogLevel_LOG_LEVEL_INFO, "too late")
	c.r.Progress(dumping, 1, 1)
	f.sink.quiet(t)
}

// --- RunningIDs

func TestRunningIDsListQueuedAndRunningCommands(t *testing.T) {
	f := setup(t, nil)
	f.e.Submit(backup("c1"))
	f.e.Submit(backup("c2"))
	c := f.files.next(t)
	if got := strings.Join(f.e.RunningIDs(), ","); got != "c1,c2" {
		t.Fatalf("RunningIDs = %s", got)
	}
	c.finish(snapshot("s1"), nil)
	f.files.next(t)
	if got := strings.Join(f.e.RunningIDs(), ","); got != "c2" {
		t.Fatalf("RunningIDs = %s", got)
	}
}

// --- shutdown (Oracle-style modes)

func TestImmediateShutdownFailsQueuedStepsAndCancelsRunningOnes(t *testing.T) {
	f := setup(t, nil)
	f.e.Submit(backup("c1"))
	accepted(t, f.sink, "c1")
	c := f.files.next(t)
	f.e.Submit(backup("c2"))
	accepted(t, f.sink, "c2")

	if err := f.e.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-c.ctx.Done()
	got := map[string]*agentv1.StepResult{}
	for range 2 {
		r := f.sink.result(t)
		got[r.GetCommandId()] = r
	}
	wantResult(t, got["c1"], "c1", failed, "agent is shutting down")
	wantResult(t, got["c2"], "c2", failed, "agent is shutting down")
	f.e.Submit(backup("c3"))
	f.sink.quiet(t)
	f.files.idle(t)
	if !strings.Contains(f.log.String(), "step ignored") || !strings.Contains(f.log.String(), "c3") {
		t.Fatalf("log:\n%s", f.log)
	}
	f.e.Submit(backup("c1")) // a known command is still answered
	wantResult(t, f.sink.result(t), "c1", failed, "agent is shutting down")
}

func TestImmediateShutdownGivesUpOnAStubbornHandlerWhenItsContextEnds(t *testing.T) {
	f := setup(t, nil)
	s := backup("c1")
	s.Plugin = "stubborn"
	f.e.Submit(s)
	accepted(t, f.sink, "c1")
	f.stubborn.next(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := f.e.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close = %v", err)
	}
	wantResult(t, f.sink.result(t), "c1", failed, "agent is shutting down; the plugin did not stop before shutdown")
}

func TestNormalShutdownLetsQueuedAndRunningStepsFinish(t *testing.T) {
	f := setup(t, nil)
	f.e.Submit(backup("c1"))
	accepted(t, f.sink, "c1")
	f.e.Submit(backup("c2"))
	accepted(t, f.sink, "c2")
	done := make(chan error, 1)
	go func() { done <- f.e.Shutdown(context.Background(), executor.ShutdownNormal) }()

	f.files.next(t).finish(snapshot("s1"), nil)
	wantResult(t, f.sink.result(t), "c1", succeeded, "")
	f.files.next(t).finish(snapshot("s2"), nil)
	wantResult(t, f.sink.result(t), "c2", succeeded, "")
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(waitLimit):
		t.Fatal("normal shutdown did not return once idle")
	}
}

func TestNormalShutdownReturnsWhenItsContextEndsWithoutCancellingSteps(t *testing.T) {
	f := setup(t, nil)
	f.e.Submit(backup("c1"))
	accepted(t, f.sink, "c1")
	c := f.files.next(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := f.e.Shutdown(ctx, executor.ShutdownNormal); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown = %v", err)
	}
	if c.ctx.Err() != nil {
		t.Fatal("normal shutdown cancelled a running step")
	}
}

func TestAbortReturnsAtOnceWithoutResults(t *testing.T) {
	f := setup(t, nil)
	f.e.Submit(backup("c1"))
	accepted(t, f.sink, "c1")
	c := f.files.next(t)
	f.e.Submit(backup("c2"))
	accepted(t, f.sink, "c2")
	if err := f.e.Shutdown(context.Background(), executor.ShutdownAbort); err != nil {
		t.Fatal(err)
	}
	<-c.ctx.Done() // running steps are told to stop, e.g. to release restic locks
	f.sink.quiet(t)
	f.files.idle(t)
	if n := f.clock.pending(); n != 0 {
		t.Fatalf("%d timers armed after abort", n)
	}
	if err := f.e.Close(context.Background()); err != nil { // nothing is left to wait for
		t.Fatal(err)
	}
}

func TestClosingAnIdleExecutorReturnsAtOnceAndCanBeRepeated(t *testing.T) {
	f := setup(t, nil)
	for range 2 {
		ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
		if err := f.e.Close(ctx); err != nil {
			t.Fatal(err)
		}
		cancel()
	}
}

func TestDefaultsAreSixteenWaitingStepsAndADayOfRuntime(t *testing.T) {
	clock, sink, files := newClock(), newSink(), newHandler(false)
	e, err := executor.New(executor.Options{
		Handlers: registry{"files": files}, Sink: sink, StateDir: t.TempDir(),
		Repositories: []string{"main"}, Clock: clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.Shutdown(context.Background(), executor.ShutdownAbort) }()
	for i := range 18 { // one runs, sixteen wait, the last is one too many
		e.Submit(backup(fmt.Sprintf("c%02d", i)))
	}
	for range 17 {
		sink.progress(t)
	}
	wantResult(t, sink.result(t), "c17", rejected, "the queue is full (16 steps waiting)")
	files.next(t)
	clock.Advance(24*time.Hour - time.Nanosecond)
	sink.quiet(t)
	clock.Advance(time.Nanosecond)
	wantResult(t, sink.result(t), "c00", timedOut, "exceeded the agent's maximum step timeout of 24h0m0s")
}

func TestAnExecutorWithOnlyRequiredOptionsRunsOneStepAtATime(t *testing.T) {
	sink, files := newSink(), newHandler(false)
	e, err := executor.New(executor.Options{
		Handlers:     registry{"files": files},
		Sink:         sink,
		StateDir:     t.TempDir(),
		Repositories: []string{"main"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = e.Shutdown(context.Background(), executor.ShutdownAbort) }()
	e.Cancel("unknown") // the default logger swallows the warning
	e.Submit(backup("c1"))
	e.Submit(backup("c2"))
	c := files.next(t)
	files.idle(t)
	c.finish(snapshot("s1"), nil)
	files.next(t)
}

func TestNewRequiresHandlersSinkAndStateDir(t *testing.T) {
	cases := map[string]func(*executor.Options){
		"handlers":  func(o *executor.Options) { o.Handlers = nil },
		"sink":      func(o *executor.Options) { o.Sink = nil },
		"state dir": func(o *executor.Options) { o.StateDir = "" },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			opts := executor.Options{Handlers: registry{}, Sink: newSink(), StateDir: t.TempDir()}
			edit(&opts)
			if _, err := executor.New(opts); !errors.Is(err, executor.ErrInvalidOptions) || !strings.Contains(err.Error(), name) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}
