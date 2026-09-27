// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package executor

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"slices"
	"time"

	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type state int

const (
	fresh     state = iota // being validated
	queued                 // ACCEPTED, waiting for a slot
	running                // handler running
	stopping               // handler cancelled, not yet returned
	finished               // result recorded
	abandoned              // dropped by an abort; no result
)

// command is one command_id and everything the executor knows about it.
type command struct {
	step    *agentv1.RunStep
	handler Handler
	state   state

	cancel  context.CancelCauseFunc
	cause   *stopCause
	timer   Timer
	grace   Timer
	checks  int
	waited  time.Duration
	started time.Time

	progress *agentv1.StepProgress // last sent
	sentAt   time.Time
	result   *agentv1.StepResult
	acked    bool
	ackedAt  time.Time
}

func (c *command) live() bool { return c.state == running || c.state == stopping }

// stopCause is why a running step was cancelled; it decides the status.
type stopCause struct {
	status  agentv1.StepStatus
	message string
}

func (s *stopCause) Error() string { return s.message }

const shutdownMessage = "agent is shutting down"

func cancelledByServer() *stopCause {
	return &stopCause{agentv1.StepStatus_STEP_STATUS_CANCELLED, "cancelled by the server"}
}

func shuttingDown() *stopCause {
	return &stopCause{agentv1.StepStatus_STEP_STATUS_FAILED, shutdownMessage}
}

func timedOut(message string, limit time.Duration) *stopCause {
	return &stopCause{agentv1.StepStatus_STEP_STATUS_TIMED_OUT, fmt.Sprintf(message, limit)}
}

// accept validates a new step and either queues it (ACCEPTED) or rejects it.
func (e *Executor) accept(step *agentv1.RunStep) {
	c := &command{step: step}
	e.cmds[step.GetCommandId()] = c
	if reason := e.check(c); reason != "" {
		e.finish(c, e.outcome(c, agentv1.StepStatus_STEP_STATUS_REJECTED, reason))
		return
	}
	c.state = queued
	e.queue = append(e.queue, c)
	e.send(c, e.opts.Clock.Now(), agentv1.StepPhase_STEP_PHASE_ACCEPTED, 0, 0)
	e.dispatch()
}

// check returns why the step cannot run, or "".
func (e *Executor) check(c *command) string {
	step := c.step
	h, ok := e.opts.Handlers.Handler(step.GetPlugin())
	if !ok {
		return fmt.Sprintf("unknown plugin %q", step.GetPlugin())
	}
	c.handler = h
	if step.GetAction() == agentv1.Action_ACTION_UNSPECIFIED || !slices.Contains(h.Actions(), step.GetAction()) {
		return fmt.Sprintf("plugin %q does not support %s", step.GetPlugin(), step.GetAction())
	}
	if reason := e.checkRepository(step); reason != "" {
		return reason
	}
	if len(e.queue) >= e.opts.MaxQueue {
		return fmt.Sprintf("the queue is full (%d steps waiting)", len(e.queue))
	}
	return ""
}

// checkRepository: data actions need a known repository; a script may name one or none.
func (e *Executor) checkRepository(step *agentv1.RunStep) string {
	name := step.GetRepositoryName()
	if name != "" && !e.repos[name] {
		return fmt.Sprintf("unknown repository %q", name)
	}
	if name == "" && step.GetAction() != agentv1.Action_ACTION_RUN {
		return fmt.Sprintf("%s needs a repository", step.GetAction())
	}
	return ""
}

// repeat answers a duplicate command_id without running anything.
func (e *Executor) repeat(c *command) {
	switch {
	case c.state == finished:
		e.opts.Sink.Result(c.result)
	case c.progress != nil:
		e.opts.Sink.Progress(c.progress)
	}
}

// dispatch starts the next queued step if a slot is free. Every event adds one
// step to the queue or frees one slot, so at most one start is ever due.
func (e *Executor) dispatch() {
	if e.active < e.opts.MaxParallel && len(e.queue) > 0 {
		c := e.queue[0]
		e.queue = e.queue[1:]
		e.start(c)
	}
}

func (e *Executor) dequeue(c *command) {
	e.queue = slices.DeleteFunc(e.queue, func(q *command) bool { return q == c })
}

func (e *Executor) start(c *command) {
	ctx, cancel := context.WithCancelCause(context.Background())
	c.state, c.cancel, c.started = running, cancel, e.opts.Clock.Now()
	e.active++
	limit, cause := e.timeout(c.step)
	c.timer = e.opts.Clock.AfterFunc(limit, func() { e.expire(c, cause) })
	go e.run(ctx, c)
}

// timeout is the step's own timeout, or the agent's maximum if it has none.
func (e *Executor) timeout(step *agentv1.RunStep) (time.Duration, *stopCause) {
	if d := step.GetTimeout().AsDuration(); d > 0 { // nil or zero: no timeout of its own
		return d, timedOut("exceeded the step timeout of %s", d)
	}
	return e.opts.MaxTimeout, timedOut("exceeded the agent's maximum step timeout of %s", e.opts.MaxTimeout)
}

func (e *Executor) expire(c *command, cause *stopCause) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.interrupt(c, cause)
}

func (e *Executor) run(ctx context.Context, c *command) {
	ret := e.call(ctx, c)
	e.mu.Lock()
	defer e.mu.Unlock()
	if !c.live() {
		e.opts.Logger.Warn("late return of a plugin ignored", "command_id", c.step.GetCommandId())
		return
	}
	e.finish(c, e.verdict(c, ret))
}

// returned is what a handler call ended with.
type returned struct {
	result   *agentv1.StepResult
	err      error
	panicked any
}

// call runs the handler, turning a panic into a value.
func (e *Executor) call(ctx context.Context, c *command) (ret returned) {
	defer func() {
		if v := recover(); v != nil {
			ret.panicked = v
			e.opts.Logger.Error("plugin panicked", "command_id", c.step.GetCommandId(), "panic", v, "stack", string(debug.Stack()))
		}
	}()
	ret.result, ret.err = c.handler.Run(ctx, c.step, &reporter{e: e, c: c})
	return ret
}

// verdict turns what the handler returned into the step's result.
func (e *Executor) verdict(c *command, ret returned) *agentv1.StepResult {
	var r *agentv1.StepResult
	switch {
	case ret.panicked != nil:
		r = e.outcome(c, agentv1.StepStatus_STEP_STATUS_FAILED, fmt.Sprintf("plugin panicked: %v", ret.panicked))
	case ret.err == nil:
		r = e.outcome(c, agentv1.StepStatus_STEP_STATUS_SUCCEEDED, ret.result.GetMessage())
	case c.cause != nil:
		r = e.outcome(c, c.cause.status, c.cause.message)
	case errors.Is(ret.err, ErrRejected):
		r = e.outcome(c, agentv1.StepStatus_STEP_STATUS_REJECTED, ret.err.Error())
	default:
		r = e.outcome(c, agentv1.StepStatus_STEP_STATUS_FAILED, ret.err.Error())
	}
	if ret.result != nil {
		r.Output = ret.result.Output
	}
	return r
}

// outcome builds a result; started_at only if the handler was started.
func (e *Executor) outcome(c *command, status agentv1.StepStatus, message string) *agentv1.StepResult {
	r := &agentv1.StepResult{
		CommandId:  c.step.GetCommandId(),
		Status:     status,
		Message:    message,
		FinishedAt: timestamppb.New(e.opts.Clock.Now()),
	}
	if !c.started.IsZero() {
		r.StartedAt = timestamppb.New(c.started)
	}
	return r
}

// interrupt cancels a running step and starts watching for its handler to return.
func (e *Executor) interrupt(c *command, cause *stopCause) {
	if c.state != running {
		return
	}
	c.state, c.cause = stopping, cause
	c.cancel(cause)
	e.watch(c, e.opts.CancelGrace)
}

// watch checks after delay whether the handler returned, doubling the delay
// up to CancelChecks times, then gives up on it.
func (e *Executor) watch(c *command, delay time.Duration) {
	c.grace = e.opts.Clock.AfterFunc(delay, func() { e.recheck(c, delay) })
}

func (e *Executor) recheck(c *command, delay time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if c.state != stopping {
		return
	}
	c.checks++
	c.waited += delay
	e.opts.Logger.Warn("plugin has not stopped after cancel", "command_id", c.step.GetCommandId(),
		"check", c.checks, "of", e.opts.CancelChecks, "waited", c.waited)
	if c.checks < e.opts.CancelChecks {
		e.watch(c, 2*delay)
		return
	}
	e.giveUp(c, fmt.Sprintf("%s; the plugin did not stop within %s", c.cause.message, c.waited))
}

// giveUp records a stopping step without waiting for its handler; the slot is freed.
func (e *Executor) giveUp(c *command, message string) {
	e.opts.Logger.Warn("giving up on a plugin that ignores cancellation", "command_id", c.step.GetCommandId())
	e.finish(c, e.outcome(c, c.cause.status, message))
}

// finish records the result, reports it and frees the slot.
func (e *Executor) finish(c *command, r *agentv1.StepResult) {
	e.stopTimers(c)
	if c.live() {
		e.active--
	}
	c.state, c.result = finished, r
	if err := e.store.saveResult(r); err != nil {
		e.opts.Logger.Error("cannot save the result; it is kept in memory only", "command_id", r.GetCommandId(), "error", err)
	}
	e.opts.Sink.Result(r)
	e.dispatch()
	e.signalIdle()
}

func (e *Executor) stopTimers(c *command) {
	for _, t := range []Timer{c.timer, c.grace} {
		if t != nil {
			t.Stop()
		}
	}
}

// send reports progress and remembers it for duplicates and rate limiting.
func (e *Executor) send(c *command, now time.Time, phase agentv1.StepPhase, processed, total uint64) {
	p := &agentv1.StepProgress{
		CommandId:      c.step.GetCommandId(),
		Phase:          phase,
		BytesProcessed: processed,
		BytesTotal:     total,
		SentAt:         timestamppb.New(now),
	}
	c.progress, c.sentAt = p, now
	e.opts.Sink.Progress(p)
}
