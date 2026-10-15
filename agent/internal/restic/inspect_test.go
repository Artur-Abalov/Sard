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

// The restic commands of repo check and repo unlock (A8d-1, Ч5-Ч7, Ч23).

func TestAReadOnlyCopyReadsTheConfigWithoutLockOrCache(t *testing.T) {
	f := newFixture(t, map[string]reply{"cat": catConfig})
	id, err := f.build().WithReadOnly().ID(context.Background())
	if id != goldenRepoID || err != nil {
		t.Fatalf("ID = %q, %v", id, err)
	}
	if got := f.exec.call("cat").Args; !slices.Equal(got, []string{"cat", "config", "--no-lock", "--no-cache"}) {
		t.Errorf("args %q", got)
	}
}

func TestTheOriginalStillTakesTheLockAfterAReadOnlyCopyWasMade(t *testing.T) {
	f := newFixture(t, map[string]reply{"cat": catConfig})
	cli := f.build()
	_ = cli.WithReadOnly()
	if _, err := cli.ID(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.exec.call("cat").Args; !slices.Equal(got, []string{"cat", "config"}) {
		t.Errorf("args %q", got)
	}
}

func TestListLocksReadsTheIdsWithoutLockOrCache(t *testing.T) {
	f := newFixture(t, map[string]reply{"list": {inline: "aa11\n\nbb22\n"}})
	ids, err := f.build().ListLocks(context.Background())
	if err != nil || !slices.Equal(ids, []string{"aa11", "bb22"}) {
		t.Fatalf("ListLocks = %q, %v", ids, err)
	}
	if got := f.exec.call("list").Args; !slices.Equal(got, []string{"list", "locks", "--no-lock", "--no-cache"}) {
		t.Errorf("args %q", got)
	}
}

func TestCatLockReturnsTheJSONOfOneLockWithoutLockOrCache(t *testing.T) {
	f := newFixture(t, map[string]reply{"cat": {inline: `{"hostname":"db1"}`}})
	data, err := f.build().CatLock(context.Background(), "aa11")
	if err != nil || string(data) != `{"hostname":"db1"}`+"\n" {
		t.Fatalf("CatLock = %q, %v", data, err)
	}
	if got := f.exec.call("cat").Args; !slices.Equal(got, []string{"cat", "lock", "--no-lock", "--no-cache", "--", "aa11"}) {
		t.Errorf("args %q", got)
	}
}

func TestUnlockRemovesNothingButWhatResticCallsStale(t *testing.T) {
	f := newFixture(t, map[string]reply{"unlock": {}})
	if err := f.build().Unlock(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.exec.call("unlock").Args; !slices.Equal(got, []string{"unlock"}) {
		t.Errorf("args %q", got)
	}
}

func TestCheckDataReadsAPercentOfThePacks(t *testing.T) {
	f := newFixture(t, map[string]reply{"check": {}})
	if err := f.build().CheckData(context.Background(), 5); err != nil {
		t.Fatal(err)
	}
	if got := f.exec.call("check").Args; !slices.Equal(got, []string{"check", "--read-data-subset=5%"}) {
		t.Errorf("args %q", got)
	}
}

func TestTheNewCommandsReportTheExitCodesOfRestic(t *testing.T) {
	f := newFixture(t, map[string]reply{"list": {code: 12}, "unlock": {code: 11}, "check": {code: 10}})
	cli := f.build()
	_, err := cli.ListLocks(context.Background())
	if !errors.Is(err, restic.ErrWrongPassword) {
		t.Errorf("list: %v", err)
	}
	if err := cli.Unlock(context.Background()); !errors.Is(err, restic.ErrLocked) {
		t.Errorf("unlock: %v", err)
	}
	if err := cli.CheckData(context.Background(), 5); !errors.Is(err, restic.ErrNoRepository) {
		t.Errorf("check: %v", err)
	}
}

func TestCatLockReportsTheExitCodeOfRestic(t *testing.T) {
	f := newFixture(t, map[string]reply{"cat": {code: 12}})
	data, err := f.build().CatLock(context.Background(), "aa11")
	if !errors.Is(err, restic.ErrWrongPassword) || data != nil {
		t.Errorf("CatLock = %q, %v", data, err)
	}
}
