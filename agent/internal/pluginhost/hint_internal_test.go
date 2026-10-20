// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/consolerestore"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// F1: the agent knows a missing program of any plugin by the SDK error, not by
// the words of one plugin.
func TestAMissingProgramIsKnownByTheSDKErrorNotByItsWords(t *testing.T) {
	byWords := errors.New("pg_dump not found in the PATH of the agent")
	if got := withCheckHint(byWords); got != byWords {
		t.Errorf("a message that only reads like a missing program got the hint: %q", got)
	}
	bySentinel := fmt.Errorf("mydb: %w: %q", sdk.ErrProgramMissing, "mongodump")
	got := withCheckHint(bySentinel)
	if !strings.HasSuffix(got.Error(), "\n"+checkHint) || !errors.Is(got, sdk.ErrProgramMissing) {
		t.Errorf("a plugin's missing program got no hint: %q", got)
	}
}

// A missing file is not a missing password file: only the file of the
// repository's password is something sard-agent check explains.
func TestAMissingFileThatIsNotThePasswordFileGetsNoHint(t *testing.T) {
	other := fmt.Errorf("open /etc/hosts: %w", fs.ErrNotExist)
	if got := withCheckHint(other); got != other {
		t.Errorf("a missing file got the hint: %q", got)
	}
	noFile := fmt.Errorf("%w of repository %q", restic.ErrPasswordFile, "main")
	if got := withCheckHint(noFile); got != noFile {
		t.Errorf("a password file error that is not a missing file got the hint: %q", got)
	}
}

// A8d-4 Ч28 with R2 and R2b: a failed console restore ends with the hint for
// the reasons sard-agent check explains, each one, and for no other.
func TestAFailedConsoleRestoreEndsWithTheCheckHintForEveryReasonCheckExplains(t *testing.T) {
	for _, reason := range []refusal.Reason{
		refusal.BackendUnavailable, refusal.StorageAccessDenied, refusal.S3KeyRejected,
		refusal.SSHKeyNotAuthorized, refusal.WrongPassword, refusal.PasswordFileMissing,
		refusal.PGClientMissing,
	} {
		t.Run(string(reason), func(t *testing.T) {
			failure := &consolerestore.Failure{Reason: reason, Stage: consolerestore.StageCheck, Detail: "x"}
			got := withCheckHint(failure)
			want := failure.Error() + "\n" + checkHint
			if got.Error() != want || !errors.Is(got, error(failure)) {
				t.Errorf("message %q, want %q", got.Error(), want)
			}
		})
	}
}

func TestAFailedConsoleRestoreOfAnotherReasonGetsNoHint(t *testing.T) {
	for _, reason := range []refusal.Reason{
		refusal.TargetNotEmpty, refusal.RestoreIncomplete, refusal.PGConnect, refusal.PGClientTooOld, refusal.PGServerTooOld,
		refusal.CreatedbRequired, refusal.SuperuserRequired, refusal.DatabaseExists, refusal.PGRestoreFailed, refusal.CleanupFailed,
		refusal.ResticFailed, refusal.TargetWrite,
	} {
		t.Run(string(reason), func(t *testing.T) {
			failure := &consolerestore.Failure{Reason: reason, Stage: consolerestore.StageCheck, Detail: "x"}
			if got := withCheckHint(failure); got != error(failure) {
				t.Errorf("message %q", got.Error())
			}
		})
	}
}

func TestNoFailureHasNoHint(t *testing.T) {
	if got := withCheckHint(nil); got != nil {
		t.Errorf("withCheckHint(nil) = %v", got)
	}
}
