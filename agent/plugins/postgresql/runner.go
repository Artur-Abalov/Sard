// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"context"
	"io"
)

// Runner runs a process to completion.
type Runner interface {
	// Run starts c and returns its exit code. A process that did not start
	// or was killed by a signal has code -1 and an error saying why (such as
	// "signal: killed"). When ctx is done the process must be stopped.
	Run(ctx context.Context, c Cmd) (code int, err error)
}

// Cmd is one process invocation.
type Cmd struct {
	Path string
	Args []string
	// Env is the complete environment; nothing is inherited from the agent.
	Env []string
	// Stdout receives the process's standard output as it is written; nil
	// discards it. A write error stops the process.
	Stdout io.Writer
	// Stderr receives each line of the standard error, without its line
	// ending; nil discards it.
	Stderr func(line string)
}
