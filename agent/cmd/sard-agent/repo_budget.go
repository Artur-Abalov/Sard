// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/repoconnect"
)

// repoContext bounds the whole command: it ends when timeout runs out on
// clk (cause repoinit.ErrTimeout) or when ctx ends (an interrupt).
func repoContext(ctx context.Context, clk repoconnect.TimeClock, timeout time.Duration) (context.Context, context.CancelCauseFunc) {
	ctx, b := repoconnect.NewBudget(ctx, clk, timeout)
	return ctx, b.Cancel
}
