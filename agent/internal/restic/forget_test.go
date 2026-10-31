// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// docs/specs/agent/retention-forget.feature (L1a): restic forget gets
// snapshot ids only (КL16) and waits for a live lock (ДL7, КL12).

func TestForgetGetsOnlyTheSnapshotIDsAfterTheDoubleDash(t *testing.T) {
	f := newFixture(t, map[string]reply{"forget": {}})
	if err := f.build().Forget(context.Background(), []string{"aa11", "-bb22"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"forget", "--retry-lock", "5m", "--", "aa11", "-bb22"}
	if got := f.exec.call("forget").Args; !slices.Equal(got, want) {
		t.Errorf("args %q, want %q", got, want)
	}
}

func TestForgetWithoutSnapshotsIsNotRun(t *testing.T) {
	f := newFixture(t, map[string]reply{})
	if err := f.build().Forget(context.Background(), nil); !errors.Is(err, restic.ErrInvalidRequest) {
		t.Errorf("err = %v", err)
	}
	if len(f.exec.calls) != 0 {
		t.Errorf("restic ran %d times", len(f.exec.calls))
	}
}

const holderLine = "unable to create lock in backend: repository is already locked exclusively by PID 4242 on db2 by sard-agent (UID 0, GID 0)"

func TestALockedRepositoryNamesItsHolder(t *testing.T) {
	f := newFixture(t, map[string]reply{"forget": {code: 11}})
	f.exec.lines = []string{holderLine, "lock was created at 2026-10-31 10:25:35 (2.9s ago)"}
	err := f.build().Forget(context.Background(), []string{"aa11"})
	if !errors.Is(err, restic.ErrLocked) {
		t.Fatalf("err = %v", err)
	}
	if got, want := restic.LockHolder(err), "PID 4242 on db2 by sard-agent (UID 0, GID 0)"; got != want {
		t.Errorf("holder = %q, want %q", got, want)
	}
	if want := "restic forget: " + restic.ErrLocked.Error(); err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}

func TestAHolderIsReadFromTheSharedLockWording(t *testing.T) {
	f := newFixture(t, map[string]reply{"forget": {code: 11}})
	f.exec.lines = []string{"unable to create lock in backend: repository is already locked by PID 7 on db3 by root (UID 0, GID 0)"}
	err := f.build().Forget(context.Background(), []string{"aa11"})
	if got := restic.LockHolder(err); got != "PID 7 on db3 by root (UID 0, GID 0)" {
		t.Errorf("holder = %q", got)
	}
}

func TestALockedExitWithoutAWordFromResticHasNoHolder(t *testing.T) {
	f := newFixture(t, map[string]reply{"forget": {code: 11}})
	err := f.build().Forget(context.Background(), []string{"aa11"})
	if !errors.Is(err, restic.ErrLocked) || restic.LockHolder(err) != "" {
		t.Errorf("err = %v, holder %q", err, restic.LockHolder(err))
	}
	if restic.LockHolder(errors.New("other")) != "" {
		t.Error("holder of another error")
	}
}

// OQ-156: restic cat has --retry-lock too; a backup waits for an exclusive
// lock where it used to fail at once.
func TestABackupWaitsForAnExclusiveLockBeforeItReadsTheRepositoryID(t *testing.T) {
	f := backupFixture(t, reply{stdout: "backup-ok.stdout"})
	if _, err := f.build().Backup(context.Background(), request, nil); err != nil {
		t.Fatal(err)
	}
	want := []string{"cat", "config", "--retry-lock", "5m"}
	if got := f.exec.call("cat").Args; !slices.Equal(got, want) {
		t.Errorf("args %q, want %q", got, want)
	}
}
