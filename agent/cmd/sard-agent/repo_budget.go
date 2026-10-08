// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"sync"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
)

// budget is the time --timeout allows a command. Time the operator spends
// at the terminal is not counted (Р33): the timer stops while the command
// waits for an answer and goes on with what was left.
type budget struct {
	clk    clock
	cancel context.CancelCauseFunc
	ctx    context.Context

	mu        sync.Mutex
	remaining time.Duration
	armedAt   time.Time
	disarm    chan struct{}
}

// newBudget bounds ctx by timeout on clk: the context ends with the cause
// repoinit.ErrTimeout when it runs out.
func newBudget(ctx context.Context, clk clock, timeout time.Duration) (context.Context, *budget) {
	ctx, cancel := context.WithCancelCause(ctx)
	b := &budget{clk: clk, cancel: cancel, ctx: ctx, remaining: timeout}
	b.arm()
	return ctx, b
}

// arm starts the timer for what is left.
func (b *budget) arm() {
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
func (b *budget) pause() {
	b.mu.Lock()
	defer b.mu.Unlock()
	close(b.disarm)
	b.remaining = max(b.remaining-b.clk.Now().Sub(b.armedAt), 0)
}

// repoContext bounds the whole command: it ends when timeout runs out on
// clk (cause repoinit.ErrTimeout) or when ctx ends (an interrupt).
func repoContext(ctx context.Context, clk clock, timeout time.Duration) (context.Context, context.CancelCauseFunc) {
	ctx, b := newBudget(ctx, clk, timeout)
	return ctx, b.cancel
}

// waiting is a terminal whose waiting is not counted in the budget.
type waiting struct {
	hostsetup.Terminal
	b *budget
}

func (w waiting) ReadSecret(prompt string) ([]byte, error) {
	w.b.pause()
	defer w.b.arm()
	return w.Terminal.ReadSecret(prompt)
}
