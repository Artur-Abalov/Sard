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
