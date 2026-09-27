// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package executor_test

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/executor"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

const waitLimit = 5 * time.Second

// --- clock

type fakeTimer struct {
	clock   *fakeClock
	at      time.Time
	f       func()
	stopped bool
	fired   bool
}

// fakeClock moves only when the test calls Advance; due timers fire in order.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

func newClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) AfterFunc(d time.Duration, f func()) executor.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{clock: c, at: c.now.Add(d), f: f}
	c.timers = append(c.timers, t)
	return t
}

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	active := !t.stopped && !t.fired
	t.stopped = true
	return active
}

// Advance moves time forward by d, firing every timer that falls due, including
// timers scheduled by the callbacks themselves.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	target := c.now.Add(d)
	c.mu.Unlock()
	for {
		t := c.nextDue(target)
		if t == nil {
			break
		}
		t.f()
	}
	c.mu.Lock()
	c.now = target
	c.mu.Unlock()
}

func (c *fakeClock) nextDue(target time.Time) *fakeTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	var due *fakeTimer
	for _, t := range c.timers {
		if !t.stopped && !t.fired && !t.at.After(target) && (due == nil || t.at.Before(due.at)) {
			due = t
		}
	}
	if due != nil {
		due.fired = true
		c.now = due.at
	}
	return due
}

// --- sink

type event struct {
	progress *agentv1.StepProgress
	result   *agentv1.StepResult
	logID    string
	log      *agentv1.LogLine
}

// fakeSink records everything the executor reports, in order.
type fakeSink struct {
	events chan event
}

func newSink() *fakeSink { return &fakeSink{events: make(chan event, 10000)} }

func (s *fakeSink) Progress(p *agentv1.StepProgress) { s.events <- event{progress: p} }
func (s *fakeSink) Result(r *agentv1.StepResult)     { s.events <- event{result: r} }
func (s *fakeSink) Log(id string, l *agentv1.LogLine) {
	s.events <- event{logID: id, log: l}
}

func (s *fakeSink) next(t *testing.T) event {
	t.Helper()
	select {
	case e := <-s.events:
		return e
	case <-time.After(waitLimit):
		t.Fatal("no event from the executor")
		return event{}
	}
}

func (s *fakeSink) progress(t *testing.T) *agentv1.StepProgress {
	t.Helper()
	e := s.next(t)
	if e.progress == nil {
		t.Fatalf("want progress, got %+v", e)
	}
	return e.progress
}

func (s *fakeSink) result(t *testing.T) *agentv1.StepResult {
	t.Helper()
	e := s.next(t)
	if e.result == nil {
		t.Fatalf("want a result, got %+v", e)
	}
	return e.result
}

// quiet fails if the executor reports anything within a short while.
func (s *fakeSink) quiet(t *testing.T) {
	t.Helper()
	select {
	case e := <-s.events:
		t.Fatalf("unexpected event %+v", e)
	case <-time.After(50 * time.Millisecond):
	}
}

// --- handler

type reply struct {
	result *agentv1.StepResult
	err    error
	panics any
}

// call is one Run of the fake handler, driven by the test.
type call struct {
	step    *agentv1.RunStep
	ctx     context.Context
	r       executor.Reporter
	replies chan reply
}

func (c *call) finish(result *agentv1.StepResult, err error) {
	c.replies <- reply{result: result, err: err}
}
func (c *call) panic(v any) { c.replies <- reply{panics: v} }

// fakeHandler blocks every Run until the test answers; a stubborn one ignores cancellation.
type fakeHandler struct {
	actions  []agentv1.Action
	stubborn bool
	calls    chan *call
	runs     atomic.Int32
	running  atomic.Int32
	peak     atomic.Int32
}

func newHandler(stubborn bool) *fakeHandler {
	return &fakeHandler{
		actions:  []agentv1.Action{agentv1.Action_ACTION_BACKUP, agentv1.Action_ACTION_RUN},
		stubborn: stubborn,
		calls:    make(chan *call, 100),
	}
}

func (h *fakeHandler) Actions() []agentv1.Action { return h.actions }

func (h *fakeHandler) Run(ctx context.Context, step *agentv1.RunStep, r executor.Reporter) (*agentv1.StepResult, error) {
	h.runs.Add(1)
	n := h.running.Add(1)
	defer h.running.Add(-1)
	for p := h.peak.Load(); n > p && !h.peak.CompareAndSwap(p, n); p = h.peak.Load() {
	}
	c := &call{step: step, ctx: ctx, r: r, replies: make(chan reply, 1)}
	h.calls <- c
	done := ctx.Done()
	if h.stubborn {
		done = nil
	}
	select {
	case rep := <-c.replies:
		if rep.panics != nil {
			panic(rep.panics)
		}
		return rep.result, rep.err
	case <-done:
		return nil, context.Cause(ctx)
	}
}

func (h *fakeHandler) next(t *testing.T) *call {
	t.Helper()
	select {
	case c := <-h.calls:
		return c
	case <-time.After(waitLimit):
		t.Fatal("the handler was not started")
		return nil
	}
}

func (h *fakeHandler) idle(t *testing.T) {
	t.Helper()
	select {
	case c := <-h.calls:
		t.Fatalf("unexpected start of %s", c.step.GetCommandId())
	case <-time.After(50 * time.Millisecond):
	}
}

type registry map[string]*fakeHandler

func (r registry) Handler(plugin string) (executor.Handler, bool) {
	h, ok := r[plugin]
	if !ok {
		return nil, false
	}
	return h, true
}

// --- fixture

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type fixture struct {
	e        *executor.Executor
	sink     *fakeSink
	clock    *fakeClock
	files    *fakeHandler
	stubborn *fakeHandler
	log      *syncBuffer
}

func setup(t *testing.T, tune func(*executor.Options)) *fixture {
	t.Helper()
	f := &fixture{sink: newSink(), clock: newClock(), files: newHandler(false), stubborn: newHandler(true), log: &syncBuffer{}}
	opts := executor.Options{
		Handlers:     registry{"files": f.files, "stubborn": f.stubborn},
		Sink:         f.sink,
		StateDir:     t.TempDir(),
		Repositories: []string{"main"},
		Clock:        f.clock,
		Logger:       slog.New(slog.NewTextHandler(f.log, nil)),
	}
	if tune != nil {
		tune(&opts)
	}
	e, err := executor.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	f.e = e
	t.Cleanup(func() { _ = e.Shutdown(context.Background(), executor.ShutdownAbort) })
	return f
}

func backup(id string) *agentv1.RunStep {
	return &agentv1.RunStep{CommandId: id, Plugin: "files", Action: agentv1.Action_ACTION_BACKUP, RepositoryName: "main"}
}

func snapshot(id string) *agentv1.StepResult {
	return &agentv1.StepResult{Output: &agentv1.StepResult_Backup{Backup: &agentv1.BackupOutput{SnapshotId: id}}}
}

func accepted(t *testing.T, s *fakeSink, id string) {
	t.Helper()
	p := s.progress(t)
	if p.GetCommandId() != id || p.GetPhase() != agentv1.StepPhase_STEP_PHASE_ACCEPTED {
		t.Fatalf("want ACCEPTED for %s, got %v", id, p)
	}
}
