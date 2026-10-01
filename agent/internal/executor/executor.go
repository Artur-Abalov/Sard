// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package executor runs the steps the server sends: a FIFO queue with a
// parallelism limit, deduplication by command_id, timeouts, cancellation and
// progress rate limiting. Each command_id reaches a handler at most once.
//
// The executor talks to the transport (A3) through Sink and to plugins (A6)
// through Handler; both carry the generated proto types.
package executor

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// Sink receives what the executor reports; transport.Transport implements it.
// Progress and Result are called with the executor's lock held, in the order
// things happen: they must not block and must not call back into the
// executor. Log is called from the plugin's goroutine without the lock and
// may block for back pressure. A plugin's lines precede its result; a line
// from a plugin that was given up on may still arrive after the result.
type Sink interface {
	Progress(p *agentv1.StepProgress)
	Result(r *agentv1.StepResult)
	Log(commandID string, line *agentv1.LogLine)
}

// Handler runs the steps of one plugin. A6 adapts sdk.Plugin to it.
type Handler interface {
	// Actions lists the actions the plugin supports.
	Actions() []agentv1.Action
	// Run executes a step and returns a result carrying Output and optionally
	// Message; the executor fills in the rest. It must return promptly once ctx
	// is done. An invalid config or an unknown secret is reported as an error
	// wrapping ErrRejected, and only before any side effect: REJECTED promises
	// the server that nothing was done.
	Run(ctx context.Context, step *agentv1.RunStep, r Reporter) (*agentv1.StepResult, error)
}

// Reporter is how a running handler reports progress and log lines.
type Reporter interface {
	Progress(phase agentv1.StepPhase, bytesProcessed, bytesTotal uint64)
	// ProgressFiles is Progress with the number of files processed and
	// expected, for phases that count files.
	ProgressFiles(phase agentv1.StepPhase, bytesProcessed, bytesTotal, filesProcessed, filesTotal uint64)
	Log(level agentv1.LogLevel, text string)
}

// Registry finds the handler of a plugin by name.
type Registry interface {
	Handler(plugin string) (Handler, bool)
}

// Clock is the executor's time source; tests substitute a manual one.
type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) Timer
}

// Timer is a pending AfterFunc call.
type Timer interface {
	Stop() bool
}

// ErrRejected marks a handler error that means "not started": the step
// becomes REJECTED rather than FAILED.
var ErrRejected = errors.New("step rejected")

// ErrInvalidOptions is returned by New for missing required options or an unusable state dir.
var ErrInvalidOptions = errors.New("invalid executor options")

// ErrNoResult is returned by Ack for a command without a finished result.
var ErrNoResult = errors.New("no result to acknowledge")

// ShutdownMode says how Shutdown treats queued and running steps, after
// Oracle's SHUTDOWN NORMAL / IMMEDIATE / ABORT.
type ShutdownMode int

const (
	// ShutdownNormal refuses new steps and lets queued and running ones finish.
	ShutdownNormal ShutdownMode = iota
	// ShutdownImmediate fails queued steps and cancels running ones, waiting
	// for them within the grace checks and the context.
	ShutdownImmediate
	// ShutdownAbort cancels running steps and returns at once without results;
	// after a restart the server learns of them from Hello.
	ShutdownAbort
)

// Options configures an Executor. Zero values take the defaults below.
type Options struct {
	Handlers Registry // required
	Sink     Sink     // required
	StateDir string   // required: unacknowledged results live here (0700)
	// Repositories are the repository names configured on this host.
	Repositories []string

	MaxParallel      int           // steps running at once; default 1
	MaxQueue         int           // steps waiting; default 16; beyond it → REJECTED
	MaxTimeout       time.Duration // timeout of a step without one; default 24h
	CancelGrace      time.Duration // first wait for a cancelled handler; doubles per check; default 2s
	CancelChecks     int           // checks before giving up on it; default 4 (2+4+8+16 = 30s)
	ProgressInterval time.Duration // at most one progress per step per interval; default 1s
	Retention        time.Duration // how long acknowledged command_ids are remembered; default 7 days
	Clock            Clock         // default: system clock
	Logger           *slog.Logger  // default: discard
}

const (
	defaultMaxParallel      = 1
	defaultMaxQueue         = 16
	defaultMaxTimeout       = 24 * time.Hour
	defaultCancelGrace      = 2 * time.Second
	defaultCancelChecks     = 4
	defaultProgressInterval = time.Second
	defaultRetention        = 7 * 24 * time.Hour
)

// Executor runs steps. All its state lives here; it is safe for concurrent use.
type Executor struct {
	opts  Options
	repos map[string]bool
	store *store

	mu         sync.Mutex
	cmds       map[string]*command
	queue      []*command
	active     int  // running + stopping
	closed     bool // no new steps
	idle       chan struct{}
	idleClosed bool
}

// New validates the options, fills in defaults, loads the results and
// tombstones kept in StateDir and returns an idle executor. Loaded results
// are available through PendingResults for resending.
func New(opts Options) (*Executor, error) {
	if err := validate(opts); err != nil {
		return nil, err
	}
	withDefaults(&opts)
	st, err := openStore(opts.StateDir)
	if err != nil {
		return nil, err
	}
	repos := make(map[string]bool, len(opts.Repositories))
	for _, name := range opts.Repositories {
		repos[name] = true
	}
	e := &Executor{opts: opts, repos: repos, store: st, cmds: map[string]*command{}}
	if err := e.restore(); err != nil {
		return nil, err
	}
	return e, nil
}

func validate(opts Options) error {
	switch {
	case opts.Handlers == nil:
		return fmt.Errorf("%w: handlers are required", ErrInvalidOptions)
	case opts.Sink == nil:
		return fmt.Errorf("%w: sink is required", ErrInvalidOptions)
	case opts.StateDir == "":
		return fmt.Errorf("%w: state dir is required", ErrInvalidOptions)
	}
	return nil
}

func withDefaults(o *Options) {
	o.MaxParallel = orDefault(o.MaxParallel, defaultMaxParallel)
	o.MaxQueue = orDefault(o.MaxQueue, defaultMaxQueue)
	o.MaxTimeout = orDefault(o.MaxTimeout, defaultMaxTimeout)
	o.CancelGrace = orDefault(o.CancelGrace, defaultCancelGrace)
	o.CancelChecks = orDefault(o.CancelChecks, defaultCancelChecks)
	o.ProgressInterval = orDefault(o.ProgressInterval, defaultProgressInterval)
	o.Retention = orDefault(o.Retention, defaultRetention)
	if o.Clock == nil {
		o.Clock = systemClock{}
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
}

func orDefault[T int | time.Duration](v, def T) T {
	if v > 0 {
		return v
	}
	return def
}

// Submit accepts a step, or answers a repeated command_id with its current
// progress or stored result. It never blocks on a handler.
func (e *Executor) Submit(step *agentv1.RunStep) {
	id := step.GetCommandId()
	e.mu.Lock()
	defer e.mu.Unlock()
	switch c := e.cmds[id]; {
	case id == "":
		e.opts.Logger.Warn("step without command_id ignored")
	case c != nil:
		e.repeat(c)
	case e.closed:
		e.opts.Logger.Warn("agent is shutting down; step ignored", "command_id", id)
	default:
		e.accept(step)
	}
}

// Cancel stops a queued or running step; a finished one gets its real result again.
func (e *Executor) Cancel(commandID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	c := e.cmds[commandID]
	switch {
	case c == nil:
		e.opts.Logger.Warn("cancel for an unknown command ignored", "command_id", commandID)
	case c.state == queued:
		e.dequeue(c)
		e.finish(c, e.outcome(c, agentv1.StepStatus_STEP_STATUS_CANCELLED, "cancelled before it started"))
	case c.state == running:
		e.interrupt(c, cancelledByServer())
	case c.state == finished:
		e.opts.Sink.Result(c.result)
	}
}

// Ack records that the server has the result of commandID: it leaves the
// pending list and is kept as a tombstone for Retention, so a repeated
// command_id still gets the same result instead of a second run.
func (e *Executor) Ack(commandID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	c := e.cmds[commandID]
	if c == nil || c.state != finished {
		return fmt.Errorf("%w: %q", ErrNoResult, commandID)
	}
	e.sweep()
	if c.acked {
		return nil
	}
	c.acked, c.ackedAt = true, e.opts.Clock.Now()
	return e.store.acknowledge(c.result, c.ackedAt)
}

// PendingResults are the finished, unacknowledged results, oldest first, for
// resending after a reconnect or a restart.
func (e *Executor) PendingResults() []*agentv1.StepResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	var pending []*agentv1.StepResult
	for _, c := range e.cmds {
		if c.state == finished && !c.acked {
			pending = append(pending, c.result)
		}
	}
	slices.SortFunc(pending, func(a, b *agentv1.StepResult) int {
		return cmp.Or(a.GetFinishedAt().AsTime().Compare(b.GetFinishedAt().AsTime()), cmp.Compare(a.GetCommandId(), b.GetCommandId()))
	})
	return pending
}

// restore loads what the previous run left: results to resend and tombstones.
func (e *Executor) restore() error {
	found, err := e.store.load(func(path string, err error) {
		e.opts.Logger.Warn("unreadable state file kept", "path", path, "error", err)
	})
	if err != nil {
		return err
	}
	for id, st := range found {
		c := &command{step: &agentv1.RunStep{CommandId: id}, state: finished, result: st.result}
		c.acked, c.ackedAt = !st.ackedAt.IsZero(), st.ackedAt
		e.cmds[id] = c
		if c.acked {
			e.dropLeftover(id)
		}
	}
	e.sweep()
	return nil
}

// dropLeftover removes a result a crash left next to its tombstone.
func (e *Executor) dropLeftover(commandID string) {
	if err := e.store.remove(resultsDir, commandID); err != nil {
		e.opts.Logger.Warn("cannot remove an acknowledged result", "command_id", commandID, "error", err)
	}
}

// sweep forgets tombstones older than Retention.
func (e *Executor) sweep() {
	now := e.opts.Clock.Now()
	for id, c := range e.cmds {
		if c.acked && now.Sub(c.ackedAt) >= e.opts.Retention {
			delete(e.cmds, id)
			if err := e.store.forget(id); err != nil {
				e.opts.Logger.Warn("cannot remove a tombstone", "command_id", id, "error", err)
			}
		}
	}
}

// RunningIDs lists queued and running commands for Hello: the server must
// neither resend them nor mark them lost.
func (e *Executor) RunningIDs() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var ids []string
	for id, c := range e.cmds {
		if c.state == queued || c.live() {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

// Close is Shutdown in ShutdownImmediate mode.
func (e *Executor) Close(ctx context.Context) error {
	return e.Shutdown(ctx, ShutdownImmediate)
}

// Shutdown stops accepting steps and treats queued and running ones by mode.
// It returns ctx.Err() if ctx ends first; in immediate mode the steps still
// running then are recorded as FAILED.
func (e *Executor) Shutdown(ctx context.Context, mode ShutdownMode) error {
	e.mu.Lock()
	e.closed = true
	if mode == ShutdownAbort {
		e.abort()
		e.mu.Unlock()
		return nil
	}
	if mode == ShutdownImmediate {
		e.halt()
	}
	idle := e.idleSignal()
	e.mu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		if mode == ShutdownImmediate {
			e.abandon()
		}
		return ctx.Err()
	}
}

// halt fails queued steps and cancels running ones.
func (e *Executor) halt() {
	waiting := e.queue
	e.queue = nil
	for _, c := range waiting {
		e.finish(c, e.outcome(c, agentv1.StepStatus_STEP_STATUS_FAILED, shutdownMessage))
	}
	for _, c := range e.cmds {
		e.interrupt(c, shuttingDown())
	}
}

// abandon records the steps still running after an immediate shutdown ran out of time.
func (e *Executor) abandon() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, c := range e.cmds {
		if c.live() {
			e.giveUp(c, shutdownMessage+"; the plugin did not stop before shutdown")
		}
	}
}

// abort cancels everything without recording results.
func (e *Executor) abort() {
	e.queue = nil
	for _, c := range e.cmds {
		if c.live() {
			e.stopTimers(c)
			c.cancel(shuttingDown())
			c.state = abandoned
		}
	}
	e.active = 0
}

// idleSignal returns a channel closed once nothing is queued or running.
func (e *Executor) idleSignal() chan struct{} {
	if e.idle == nil {
		e.idle = make(chan struct{})
	}
	e.signalIdle()
	return e.idle
}

func (e *Executor) signalIdle() {
	// A queued step implies a busy slot, so active == 0 means nothing is queued either.
	if e.idle != nil && !e.idleClosed && e.active == 0 {
		close(e.idle)
		e.idleClosed = true
	}
}
