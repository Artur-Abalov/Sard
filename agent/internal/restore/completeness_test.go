// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restore_test

import (
	"context"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/restore"
)

// completenessOf lists the snapshots and gives the completeness by id prefix.
func completenessOf(t *testing.T, snaps ...restic.Snapshot) map[string]restore.Completeness {
	t.Helper()
	got, err := restore.List(context.Background(), &fakeLister{snapshots: snaps}, "")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]restore.Completeness{}
	for _, s := range got {
		out[s.ShortID[:2]] = s.Completeness
	}
	return out
}

func TestOnlyAFingerprintSnapshotConfirmsTheSnapshotItNames(t *testing.T) {
	got := completenessOf(t,
		// not a fingerprint snapshot, first in the list: it confirms nothing
		restic.Snapshot{ID: id("cc"), Tags: []string{"sard.run=r1", "files.main_snapshot=" + id("aa")}},
		restic.Snapshot{ID: id("aa"), Tags: []string{"sard.run=r1"}},
		restic.Snapshot{ID: id("bb"), Tags: []string{"sard.run=r1"}},
		// the part tag first: the link is still found among the later tags
		restic.Snapshot{ID: id("dd"), Tags: []string{"files.part=fingerprint", "sard.run=r1", "files.main_snapshot=" + id("bb")}},
	)
	if got["aa"] != restore.Unconfirmed || got["bb"] != restore.Confirmed {
		t.Errorf("got %v", got)
	}
}

func TestEverySnapshotIsMarkedNotOnlyTheFirst(t *testing.T) {
	got := completenessOf(t,
		restic.Snapshot{ID: id("aa"), Tags: []string{"sard.run=r1"}},
		restic.Snapshot{ID: id("bb"), Tags: []string{"sard.run=r1"}},
	)
	if got["aa"] != restore.Unconfirmed || got["bb"] != restore.Unconfirmed {
		t.Errorf("got %v", got)
	}
}

func TestCompletenessAppliesToMainSnapshotsOfASardRunOnly(t *testing.T) {
	got := completenessOf(t,
		restic.Snapshot{ID: id("aa")},
		restic.Snapshot{ID: id("bb"), Tags: []string{"sard.source=s1"}},
		restic.Snapshot{ID: id("cc"), Tags: []string{"sard.run=r1", "sard.step=st1", "other"}},
		restic.Snapshot{ID: id("dd"), Tags: []string{"sard.run=r1", "files.part=fingerprint"}},
		restic.Snapshot{ID: id("ee"), Tags: []string{"sard.run=r1", "x.y=1"}},
	)
	want := map[string]restore.Completeness{
		"aa": restore.NotApplicable, "bb": restore.NotApplicable, "cc": restore.Unconfirmed,
		"dd": restore.NotApplicable, "ee": restore.NotApplicable,
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("snapshot %s: %q, want %q", k, got[k], w)
		}
	}
}
