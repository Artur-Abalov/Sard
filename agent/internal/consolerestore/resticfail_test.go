// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

func localTarget() repoinit.Target {
	return repoinit.Target{Name: "main", Backend: "local", Scrub: func(s string) string { return s }}
}

// Spec ВК7: the reasons are those of A5b/A8c, whichever command restores.
func TestResticFailuresAreNamedAsRepoRestoreNamesThem(t *testing.T) {
	ctx := context.Background()
	for _, err := range []error{restic.ErrWrongPassword, restic.ErrEmptyPassword, restic.ErrNetwork, restic.ErrBadOutput} {
		wrapped := fmt.Errorf("x: %w", err)
		want := repoinit.FromRestic(ctx, wrapped, localTarget()).Reason
		if got := resticFailure(ctx, wrapped, StageRestore, "/r", localTarget()).Reason; got != want {
			t.Errorf("%v: console %s, repo restore %s", err, got, want)
		}
	}
}

func TestTextTakenFromResticIsScrubbedOfTheSecretsOfTheRepository(t *testing.T) {
	target := localTarget()
	target.Scrub = func(s string) string { return strings.ReplaceAll(s, "hunter2", "[hidden]") }
	f := resticFailure(context.Background(), fmt.Errorf("dial hunter2: %w", restic.ErrNetwork), StageCheck, "/r", target)
	if strings.Contains(f.Detail, "hunter2") {
		t.Errorf("the secret is in the failure: %s", f.Detail)
	}
}

func TestAFullDiskOfTheRestoreDirectoryIsNotPartialBecauseNothingStays(t *testing.T) {
	f := resticFailure(context.Background(), restic.ErrNoSpace, StageRestore, "/r", localTarget())
	if f.Reason != "TARGET_WRITE" || strings.Contains(f.Detail, "partially") || !strings.Contains(f.Detail, "/r") {
		t.Errorf("%s: %s", f.Reason, f.Detail)
	}
}

// ADR 0008: the path of a password file does not go to the server; the
// repository is named instead.
func TestAFailureSentToTheServerNamesNoPasswordFile(t *testing.T) {
	for _, err := range []error{restic.ErrWrongPassword, restic.ErrEmptyPassword} {
		f := resticFailure(context.Background(), err, StageCheck, "/r", localTarget())
		if strings.Contains(f.Detail, "  ") || strings.Contains(f.Detail, "/") {
			t.Errorf("%v: names a password file: %s", err, f.Detail)
		}
		if !strings.Contains(f.Detail, `repository "main"`) {
			t.Errorf("%v: does not name the repository: %s", err, f.Detail)
		}
	}
}

// The storage of a remote repository can refuse; a local one cannot, and what
// restic did there has no name of the storage's.
func TestOnlyARemoteStorageCanRefuse(t *testing.T) {
	remote := localTarget()
	remote.Backend, remote.Remote, remote.Where = "s3", true, "s3.example.com"
	tests := []struct {
		name   string
		target repoinit.Target
		want   string
	}{
		{"remote", remote, "BACKEND_REFUSED"},
		{"local", localTarget(), "RESTIC_FAILED"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := resticFailure(context.Background(), fmt.Errorf("boom"), StageRestore, "/r", tt.target)
			if string(f.Reason) != tt.want {
				t.Errorf("reason = %s, want %s", f.Reason, tt.want)
			}
		})
	}
}
