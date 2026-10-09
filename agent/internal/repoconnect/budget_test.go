// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoconnect_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/repoconnect"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
)

// handClock is a TimeClock that moves only when a test says so.
type handClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*handTimer
	asked  []time.Duration
}

type handTimer struct {
	at time.Time
	ch chan time.Time
}

func newHandClock() *handClock { return &handClock{now: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)} }

func (c *handClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &handTimer{at: c.now.Add(d), ch: make(chan time.Time, 1)}
	c.timers = append(c.timers, t)
	c.asked = append(c.asked, d)
	return t.ch
}

func (c *handClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *handClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	for _, t := range c.timers {
		if !t.at.After(c.now) {
			select {
			case t.ch <- c.now:
			default:
			}
		}
	}
}

func (c *handClock) lastAsked() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.asked[len(c.asked)-1]
}

// slowTerm lets a long time pass while the operator answers.
type slowTerm struct {
	clock *handClock
	wait  time.Duration
}

func (s slowTerm) ReadSecret(string) ([]byte, error) {
	s.clock.advance(s.wait)
	return []byte("x"), nil
}

func stillOpen(t *testing.T, ctx context.Context) {
	t.Helper()
	select {
	case <-ctx.Done():
		t.Fatalf("the budget ended: %v", context.Cause(ctx))
	case <-time.After(50 * time.Millisecond):
	}
}

func TestTheBudgetEndsTheContextWithTheTimeoutCauseWhenItRunsOut(t *testing.T) {
	clock := newHandClock()
	ctx, b := repoconnect.NewBudget(t.Context(), clock, 10*time.Second)
	defer b.Cancel(nil)
	clock.advance(9 * time.Second)
	stillOpen(t, ctx)
	clock.advance(time.Second)
	select {
	case <-ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("the budget did not end")
	}
	if context.Cause(ctx) != repoinit.ErrTimeout {
		t.Fatalf("cause %v", context.Cause(ctx))
	}
}

func TestTheTimeTheOperatorTakesAtTheTerminalIsNotDeducted(t *testing.T) {
	clock := newHandClock()
	ctx, b := repoconnect.NewBudget(t.Context(), clock, 10*time.Second)
	defer b.Cancel(nil)
	term := repoconnect.Waiting(slowTerm{clock: clock, wait: time.Hour}, b)
	if _, err := term.ReadSecret("p"); err != nil {
		t.Fatal(err)
	}
	// The timer that fell due while the command waited does not end it.
	stillOpen(t, ctx)
	clock.advance(10 * time.Second)
	select {
	case <-ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("the budget did not end after what was left")
	}
}

func TestATimerThatFiresAfterThePauseDoesNotCancel(t *testing.T) {
	clock := newHandClock()
	ctx, b := repoconnect.NewBudget(t.Context(), clock, 10*time.Second)
	defer b.Cancel(nil)
	// The wait is exactly the budget: the old timer falls due inside ReadSecret.
	term := repoconnect.Waiting(slowTerm{clock: clock, wait: 10 * time.Second}, b)
	for range 20 {
		if _, err := term.ReadSecret("p"); err != nil {
			t.Fatal(err)
		}
	}
	stillOpen(t, ctx)
}

func TestTheTimeLeftAfterTheWaitIsTheTimeLeftBeforeIt(t *testing.T) {
	clock := newHandClock()
	_, b := repoconnect.NewBudget(t.Context(), clock, 10*time.Second)
	defer b.Cancel(nil)
	clock.advance(4 * time.Second)
	term := repoconnect.Waiting(slowTerm{clock: clock, wait: time.Hour}, b)
	if _, err := term.ReadSecret("p"); err != nil {
		t.Fatal(err)
	}
	if got := clock.lastAsked(); got != 6*time.Second {
		t.Fatalf("timer re-armed for %v, want 6s", got)
	}
}

func TestTheTimeLeftNeverGoesBelowNothing(t *testing.T) {
	clock := newHandClock()
	_, b := repoconnect.NewBudget(t.Context(), clock, 10*time.Second)
	defer b.Cancel(nil)
	clock.advance(11 * time.Second)
	term := repoconnect.Waiting(slowTerm{clock: clock, wait: time.Second}, b)
	if _, err := term.ReadSecret("p"); err != nil {
		t.Fatal(err)
	}
	if got := clock.lastAsked(); got != 0 {
		t.Fatalf("timer re-armed for %v, want none left", got)
	}
}

func TestCancelEndsTheContextWithTheCauseGiven(t *testing.T) {
	cause := errors.New("stopped by the test")
	ctx, b := repoconnect.NewBudget(t.Context(), newHandClock(), time.Hour)
	b.Cancel(cause)
	if ctx.Err() == nil || context.Cause(ctx) != cause {
		t.Fatalf("ctx %v, cause %v", ctx.Err(), context.Cause(ctx))
	}
}

// slowLiner is a terminal that can ask a line too.
type slowLiner struct {
	slowTerm
	answer string
}

func (s slowLiner) ReadLine(string) ([]byte, error) {
	s.clock.advance(s.wait)
	return []byte(s.answer), nil
}

func TestTheTimeTheOperatorTakesToConfirmIsNotDeductedEither(t *testing.T) {
	clock := newHandClock()
	ctx, b := repoconnect.NewBudget(t.Context(), clock, 10*time.Second)
	defer b.Cancel(nil)
	confirm := repoconnect.Confirmation(slowLiner{slowTerm{clock: clock, wait: time.Hour}, "yes"}, b)
	answer, err := confirm("Trust it? ")
	if err != nil || answer != "yes" {
		t.Fatalf("answer %q, err %v", answer, err)
	}
	stillOpen(t, ctx)
	if got := clock.lastAsked(); got != 10*time.Second {
		t.Fatalf("timer re-armed for %v", got)
	}
}

func TestWithoutATerminalThatCanAskALineThereIsNoConfirmation(t *testing.T) {
	_, b := repoconnect.NewBudget(t.Context(), newHandClock(), time.Second)
	defer b.Cancel(nil)
	if repoconnect.Confirmation(nil, b) != nil {
		t.Error("a confirmation without a terminal")
	}
	if repoconnect.Confirmation(slowTerm{clock: newHandClock()}, b) != nil {
		t.Error("a confirmation by a terminal that cannot ask a line")
	}
}
