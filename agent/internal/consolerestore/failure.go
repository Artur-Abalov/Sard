// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore

import (
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
)

// Stage is where a restore failed.
type Stage string

// The stages of a restore.
const (
	StageCheck    Stage = "check"
	StageRestore  Stage = "restore"
	StageMove     Stage = "move"
	StageRollback Stage = "rollback"
)

// Failure is a restore that did not happen. Its text is
// "<REASON> (<stage>): <detail>; nothing was restored", or, when the
// rollback could not remove what the restore wrote, CLEANUP_FAILED.
type Failure struct {
	Reason refusal.Reason
	Stage  Stage
	Detail string
	// Leftover are the paths the rollback could not remove; Cleanup is why.
	Leftover []string
	Cleanup  string
}

// Error is the message of the step.
func (f *Failure) Error() string {
	cause := string(f.Reason) + " (" + string(f.Stage) + "): " + f.Detail
	if len(f.Leftover) == 0 {
		return cause + "; nothing was restored"
	}
	return string(refusal.CleanupFailed) + " (" + string(StageRollback) + "): " + cause +
		"; left behind: " + strings.Join(f.Leftover, ", ") + " (" + f.Cleanup + "); remove it by hand"
}
