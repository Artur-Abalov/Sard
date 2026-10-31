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

// docs/specs/agent/retention-prune.feature (L1 release 1): restic prune gets
// the repository flags and --retry-lock 5m, nothing else (КПр3).

// summary is the tail of restic prune as 0.19 prints it when it removed
// 1.000 GiB and left 50.000 GiB.
const summary = `to repack:             0 blobs / 0 B
this removes:          0 blobs / 0 B
to delete:          1000 blobs / 1.000 GiB
total prune:        1000 blobs / 1.000 GiB
remaining:         50000 blobs / 50.000 GiB
unused size after prune: 0 B (0.00% of remaining size)

done`

func TestPruneGetsOnlyTheRetryLockFlag(t *testing.T) {
	f := newFixture(t, map[string]reply{"prune": {inline: summary}})
	if _, err := f.build().Prune(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"prune", "--retry-lock", "5m"}
	if got := f.exec.call("prune").Args; !slices.Equal(got, want) {
		t.Errorf("args %q, want %q", got, want)
	}
}

func TestPruneReadsFreedAndRemainingBytesFromTheSummary(t *testing.T) {
	f := newFixture(t, map[string]reply{"prune": {inline: summary}})
	got, err := f.build().Prune(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Freed == nil || *got.Freed != 1073741824 || got.Size == nil || *got.Size != 53687091200 {
		t.Errorf("summary %+v, want freed 1073741824 and size 53687091200", got)
	}
	if len(got.Lines) != 7 || got.Lines[3] != "total prune:        1000 blobs / 1.000 GiB" {
		t.Errorf("lines = %q", got.Lines)
	}
}

func TestPruneReadsEveryUnitOfTheSummary(t *testing.T) {
	for line, want := range map[string]uint64{
		"0 B": 0, "512 B": 512, "1.500 KiB": 1536, "2.000 MiB": 2 << 20, "3.000 GiB": 3 << 30, "1.000 TiB": 1 << 40,
	} {
		f := newFixture(t, map[string]reply{"prune": {inline: "total prune:   5 blobs / " + line + "\nremaining: 1 blobs / " + line}})
		got, err := f.build().Prune(context.Background())
		if err != nil || got.Freed == nil || *got.Freed != want || got.Size == nil || *got.Size != want {
			t.Errorf("%s: summary %+v, err %v, want %d", line, got, err, want)
		}
	}
}

func TestPruneWithoutAParsableSummaryHasNoNumbers(t *testing.T) {
	for _, out := range []string{"done", "total prune: lots\nremaining: some", "total prune: 1 blobs / 1.0 PiB", ""} {
		f := newFixture(t, map[string]reply{"prune": {inline: out}})
		got, err := f.build().Prune(context.Background())
		if err != nil || got.Freed != nil || got.Size != nil {
			t.Errorf("%q: summary %+v, err %v, want no numbers", out, got, err)
		}
	}
}

func TestPruneOfALockedRepositoryNamesItsHolder(t *testing.T) {
	f := newFixture(t, map[string]reply{"prune": {code: 11}})
	f.exec.lines = []string{holderLine}
	_, err := f.build().Prune(context.Background())
	if !errors.Is(err, restic.ErrLocked) || restic.LockHolder(err) != "PID 4242 on db2 by sard-agent (UID 0, GID 0)" {
		t.Errorf("err = %v", err)
	}
}
