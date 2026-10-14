// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// The shape of restic snapshots --json (0.19): an array of objects with
// the time in the zone of the host that made the snapshot.
const snapshotsJSON = `[{"time":"2026-10-10T06:00:00.123456789+03:00","tree":"t","paths":["/srv/data"],"hostname":"db1","username":"root","tags":["sard.run=r1","sard.source=s1"],"id":"aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111","short_id":"aaaa1111"},` +
	`{"time":"2026-10-09T12:00:00Z","paths":["/etc","/var"],"hostname":"old-host","id":"dddd1111dddd1111dddd1111dddd1111dddd1111dddd1111dddd1111dddd1111","short_id":"dddd1111"}]`

func TestSnapshotsListsTheRepositoryWithoutTheLock(t *testing.T) {
	f := newFixture(t, map[string]reply{"snapshots": {inline: snapshotsJSON}})
	got, err := f.build().Snapshots(context.Background(), []string{"sard.source=s1"})
	if err != nil || len(got) != 2 {
		t.Fatalf("Snapshots = %v, %v", got, err)
	}
	want := []string{"snapshots", "--json", "--no-lock", "--tag", "sard.source=s1"}
	if args := f.exec.call("snapshots").Args; !slices.Equal(args, want) {
		t.Errorf("args %q, want %q", args, want)
	}
}

func TestSnapshotsReadsTheFieldsOfRestic(t *testing.T) {
	f := newFixture(t, map[string]reply{"snapshots": {inline: snapshotsJSON}})
	got, _ := f.build().Snapshots(context.Background(), nil)
	first := got[0]
	if first.ID[:8] != "aaaa1111" || first.Hostname != "db1" || !slices.Equal(first.Paths, []string{"/srv/data"}) ||
		!slices.Equal(first.Tags, []string{"sard.run=r1", "sard.source=s1"}) {
		t.Errorf("first = %+v", first)
	}
	if !first.Time.Equal(time.Date(2026, 10, 10, 3, 0, 0, 123456789, time.UTC)) {
		t.Errorf("time %v", first.Time)
	}
	if got[1].Tags != nil {
		t.Errorf("a snapshot without tags has tags %v", got[1].Tags)
	}
}

func TestSnapshotsWithoutATagAddsNoTagFlag(t *testing.T) {
	f := newFixture(t, map[string]reply{"snapshots": {inline: "[]"}})
	got, err := f.build().Snapshots(context.Background(), nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("Snapshots = %v, %v", got, err)
	}
	if args := f.exec.call("snapshots").Args; !slices.Equal(args, []string{"snapshots", "--json", "--no-lock"}) {
		t.Errorf("args %q", args)
	}
}

func TestSnapshotsRefusesOutputItCannotRead(t *testing.T) {
	f := newFixture(t, map[string]reply{"snapshots": {inline: "not json"}})
	if _, err := f.build().Snapshots(context.Background(), nil); !errors.Is(err, restic.ErrBadOutput) {
		t.Fatalf("err = %v", err)
	}
}

func TestSnapshotsReportsTheExitCodeOfRestic(t *testing.T) {
	f := newFixture(t, map[string]reply{"snapshots": {code: 12}})
	if _, err := f.build().Snapshots(context.Background(), nil); !errors.Is(err, restic.ErrWrongPassword) {
		t.Fatalf("err = %v", err)
	}
}
