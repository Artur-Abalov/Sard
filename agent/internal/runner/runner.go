// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package runner executes workflow steps and user scripts on the host.
package runner

import (
	"context"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// Step is one workflow step received from the server.
type Step struct {
	CommandID string
	Plugin    string
	Config    sdk.Config
}

// Result is the outcome of a step.
type Result struct {
	SnapshotID string
}

// Runner executes steps.
type Runner interface {
	Run(ctx context.Context, step Step) (Result, error)
}

// Stub is the placeholder Runner.
type Stub struct{}

// Run will dispatch the step to its plugin and restic (roadmap: workflow engine, stage 1).
func (Stub) Run(context.Context, Step) (Result, error) {
	return Result{}, sdk.ErrNotImplemented
}
