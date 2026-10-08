// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoconnect

import (
	"context"
	"sync"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
)

// TimeClock is a clock that can also tell the time, which the budget needs
// to count what the operator's waiting took.
type TimeClock interface {
	Clock
	Now() time.Time
}

// Budget is the time --timeout allows a command. Time the operator spends
// at the terminal is not counted (Р33): the timer stops while the command
// waits for an answer and goes on with what was left.
type Budget struct {
	clk    TimeClock
	cancel context.CancelCauseFunc
	ctx    context.Context

	mu        sync.Mutex
	remaining time.Duration
	armedAt   time.Time
	disarm    chan struct{}
}

// NewBudget bounds ctx by timeout on clk: the context ends with the cause
// repoinit.ErrTimeout when it runs out.
func NewBudget(ctx context.Context, clk TimeClock, timeout time.Duration) (context.Context, *Budget) {
	ctx, cancel := context.WithCancelCause(ctx)
	b := &Budget{clk: clk, cancel: cancel, ctx: ctx, remaining: timeout}
	b.arm()
	return ctx, b
}

// arm starts the timer for what is left.
func (b *Budget) arm() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.armedAt = b.clk.Now()
	disarm := make(chan struct{})
	b.disarm = disarm
	timer := b.clk.After(b.remaining)
	go func() {
		select {
		case <-timer:
			select {
			case <-disarm: // paused at the same moment: this timer is no longer the budget
			default:
				b.cancel(repoinit.ErrTimeout)
			}
		case <-disarm:
		case <-b.ctx.Done():
		}
	}()
}

// pause stops the timer and notes what is left.
func (b *Budget) pause() {
	b.mu.Lock()
	defer b.mu.Unlock()
	close(b.disarm)
	b.remaining = max(b.remaining-b.clk.Now().Sub(b.armedAt), 0)
}

// Cancel ends the context with cause; nil is a plain cancellation.
func (b *Budget) Cancel(cause error) { b.cancel(cause) }

// Waiting is a terminal whose waiting is not counted in the budget.
func Waiting(t hostsetup.Terminal, b *Budget) hostsetup.Terminal {
	return waiting{Terminal: t, b: b}
}

type waiting struct {
	hostsetup.Terminal
	b *Budget
}

func (w waiting) ReadSecret(prompt string) ([]byte, error) {
	w.b.pause()
	defer w.b.arm()
	return w.Terminal.ReadSecret(prompt)
}
