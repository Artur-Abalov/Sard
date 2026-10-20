// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

package sdk

import "errors"

// The reasons the host itself words for a failure of the repository, and that
// a plugin may also say. They are spelled as the agent spells them: the agent
// checks that. A plugin picks its own reasons besides (ValidReason).
const (
	ReasonBackendUnavailable     = "BACKEND_UNAVAILABLE"
	ReasonBackendRefused         = "BACKEND_REFUSED"
	ReasonStorageAccessDenied    = "STORAGE_ACCESS_DENIED"
	ReasonWrongPassword          = "WRONG_PASSWORD"
	ReasonPasswordFileMissing    = "PASSWORD_FILE_MISSING"
	ReasonRepositoryUninit       = "REPOSITORY_NOT_INITIALIZED"
	ReasonSnapshotUnknown        = "SNAPSHOT_UNKNOWN"
	ReasonResticFailed           = "RESTIC_FAILED"
	ReasonResticOutputUnexpected = "RESTIC_OUTPUT_UNEXPECTED"
	ReasonInterrupted            = "INTERRUPTED"
	ReasonTimeout                = "TIMEOUT"
	ReasonCleanupFailed          = "CLEANUP_FAILED"
)

// GenericReasons lists the reasons above.
func GenericReasons() []string {
	return []string{
		ReasonBackendUnavailable, ReasonBackendRefused, ReasonStorageAccessDenied, ReasonWrongPassword,
		ReasonPasswordFileMissing, ReasonRepositoryUninit, ReasonSnapshotUnknown, ReasonResticFailed,
		ReasonResticOutputUnexpected, ReasonInterrupted, ReasonTimeout, ReasonCleanupFailed,
	}
}

// Limits of the words a plugin gives a refusal.
const (
	maxReason = 64
	maxStage  = 32
)

// ValidReason reports whether s is a reason code: ^[A-Z][A-Z0-9_]{0,63}$.
// The agent replaces any other with PLUGIN_FAILED.
func ValidReason(s string) bool {
	return len(s) >= 1 && len(s) <= maxReason && upper(s[0]) && allBytes(s[1:], func(c byte) bool { return upper(c) || digit(c) || c == '_' })
}

// ValidStage reports whether s is a stage of a restore: ^[a-z][a-z-]{0,31}$.
func ValidStage(s string) bool {
	return len(s) >= 1 && len(s) <= maxStage && lower(s[0]) && allBytes(s[1:], func(c byte) bool { return lower(c) || c == '-' })
}

func upper(c byte) bool { return c >= 'A' && c <= 'Z' }
func lower(c byte) bool { return c >= 'a' && c <= 'z' }
func digit(c byte) bool { return c >= '0' && c <= '9' }

func allBytes(s string, ok func(byte) bool) bool {
	for i := 0; i < len(s); i++ {
		if !ok(s[i]) {
			return false
		}
	}
	return true
}

// RestoreError is a restore that did not happen. A plugin returns it from
// RestoreRunner.Restore, and passes on the ones the host returned from
// RestoreHost.Snapshots and Dump (it only sets the stage, AtStage).
//
// The agent words it for the console as
// "<Reason> (<Stage>): <Detail>; nothing was restored", or, if Leftover names
// what the rollback could not remove, as CLEANUP_FAILED with Leftover and
// Cleanup. Detail and Cleanup go to the server: no secret values in them.
type RestoreError struct {
	// Reason is a reason code (ValidReason).
	Reason string
	// Stage says where the restore stopped (ValidStage), e.g. "check".
	Stage  string
	Detail string
	// Leftover names what the rollback could not remove and the operator must;
	// Cleanup is why.
	Leftover []string
	Cleanup  string
	// Retain asks the agent to keep the journal entry of the command after
	// it recorded the result, so that the next start tries the rollback again.
	// Without it the agent removes the entry.
	Retain bool
	// Err is the cause. The agent looks for ErrProgramMissing in it.
	Err error
}

func (e *RestoreError) Error() string {
	return e.Reason + " (" + e.Stage + "): " + e.Detail
}

// Unwrap returns the cause.
func (e *RestoreError) Unwrap() error { return e.Err }

// AtStage returns err with the stage set if err is, or wraps, a *RestoreError;
// any other error is returned as it is. The error given is not changed.
func AtStage(err error, stage string) error {
	var re *RestoreError
	if !errors.As(err, &re) {
		return err
	}
	staged := *re
	staged.Stage = stage
	return &staged
}
