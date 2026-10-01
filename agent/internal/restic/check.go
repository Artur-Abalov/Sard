// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
)

// Problem says why a restic binary cannot be used.
type Problem string

// The problems Check reports.
const (
	NotFound Problem = "not found"
	TooOld   Problem = "too old"
	Unusable Problem = "unusable"
)

// CheckError is a restic binary the agent cannot use (A5b).
type CheckError struct {
	Problem Problem
	// Binary is the path that was run.
	Binary string
	// Found is the version restic printed, e.g. "0.18.1-dev" (TooOld).
	Found string
	// Minimum is the oldest accepted release (TooOld).
	Minimum Version
	// Err is the cause (NotFound, Unusable).
	Err error
}

func (e *CheckError) Error() string {
	if e.Problem == TooOld {
		return fmt.Sprintf("restic %s at %s is older than the minimum %s", e.Found, e.Binary, e.Minimum)
	}
	return fmt.Sprintf("restic at %s is %s: %v", e.Binary, e.Problem, e.Err)
}

// Unwrap lets errors.Is find fs.ErrNotExist and ErrUnsupportedVersion.
func (e *CheckError) Unwrap() error {
	if e.Problem == TooOld {
		return ErrUnsupportedVersion
	}
	return e.Err
}

// Check runs `restic version` and reports a binary that is missing, not
// runnable, or older than Minimum as a *CheckError. A cancelled context is
// returned as it is. It needs no repository or key.
func (c *CLI) Check(ctx context.Context) error {
	v, raw, err := c.probe(ctx)
	switch {
	case ctx.Err() != nil:
		return err
	case errors.Is(err, fs.ErrNotExist):
		return &CheckError{Problem: NotFound, Binary: c.opts.Binary, Err: err}
	case err != nil:
		return &CheckError{Problem: Unusable, Binary: c.opts.Binary, Err: err}
	case v.Less(Minimum):
		return &CheckError{Problem: TooOld, Binary: c.opts.Binary, Found: raw, Minimum: Minimum}
	}
	return nil
}
