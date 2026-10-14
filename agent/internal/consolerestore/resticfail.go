// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// maxNamedPaths is how many paths RESTORE_INCOMPLETE names.
const maxNamedPaths = 10

// resticKinds are the kinds of failure of restic the agent has a name for,
// in the order they are tried: the first that matches the error wins.
var resticKinds = []struct {
	matches func(err error) bool
	reason  refusal.Reason
	detail  func(err error, dir string) string
}{
	{func(err error) bool { return errors.Is(err, restic.ErrNoSpace) }, refusal.TargetWrite,
		func(_ error, dir string) string {
			return "no space left on device (or the quota is used up) while writing to " + dir
		}},
	{func(err error) bool { return errors.Is(err, restic.ErrReadOnly) }, refusal.TargetWrite,
		func(_ error, dir string) string { return "the file system of " + dir + " is read-only" }},
	{func(err error) bool { var e *restic.RestoreError; return errors.As(err, &e) }, refusal.RestoreIncomplete,
		func(err error, _ string) string {
			var e *restic.RestoreError
			_ = errors.As(err, &e)
			return incompleteText(e.Paths())
		}},
	{func(err error) bool { return errors.Is(err, restic.ErrWrongPassword) }, refusal.WrongPassword,
		func(error, string) string { return "the password of the repository does not open it" }},
	{func(err error) bool { return errors.Is(err, restic.ErrNoRepository) }, refusal.RepositoryNotInitialized,
		func(error, string) string { return "nothing is stored at the address of the repository" }},
	{func(err error) bool { return errors.Is(err, restic.ErrNetwork) }, refusal.BackendUnavailable,
		func(err error, _ string) string { return causeOf(err) }},
	{func(err error) bool { return errors.Is(err, restic.ErrBadOutput) }, refusal.ResticOutputUnexpected,
		func(err error, _ string) string { return causeOf(err) }},
}

// resticFailure tells a failure of restic by its kind. A step that was
// stopped is INTERRUPTED: the executor words the status of a cancelled or
// timed out step itself.
func resticFailure(ctx context.Context, err error, stage Stage, dir string) *Failure {
	if ctx.Err() != nil {
		return &Failure{Reason: refusal.Interrupted, Stage: stage, Detail: "the step was stopped"}
	}
	for _, k := range resticKinds {
		if k.matches(err) {
			return &Failure{Reason: k.reason, Stage: stage, Detail: k.detail(err, dir)}
		}
	}
	return &Failure{Reason: refusal.ResticFailed, Stage: stage, Detail: causeOf(err)}
}

func incompleteText(paths []string) string {
	head := fmt.Sprintf("restic could not restore %d paths", len(paths))
	if len(paths) > maxNamedPaths {
		head += fmt.Sprintf(" (the first %d)", maxNamedPaths)
		paths = paths[:maxNamedPaths]
	}
	quoted := make([]string, len(paths))
	for i, p := range paths {
		quoted[i] = fmt.Sprintf("%q", p)
	}
	return head + ": " + strings.Join(quoted, ", ")
}

// causeOf is the fatal message restic printed, or the error as it is.
func causeOf(err error) string {
	var exit *restic.ExitError
	if errors.As(err, &exit) && exit.Message != "" {
		return exit.Cause()
	}
	return strings.ReplaceAll(err.Error(), "\n", " ")
}
