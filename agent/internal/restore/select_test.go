// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restore_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/restore"
)

// Rule Р58 (A8c-2): the snapshot of repo restore.

func TestCheckRefAcceptsAnIdAPrefixAndLatestWithASource(t *testing.T) {
	for _, c := range []struct{ ref, source string }{
		{id("aaaa1111"), ""}, {"aaaa1111", ""}, {strings.Repeat("a", 63), ""}, {"latest", "s1"},
		{"00000000", ""}, {"99999999", ""}, {"ffffffff", ""}, {"09af09af", ""},
	} {
		if err := restore.CheckRef(c.ref, c.source); err != nil {
			t.Errorf("CheckRef(%q, %q) = %v", c.ref, c.source, err)
		}
	}
}

func TestCheckRefRefusesAnythingElse(t *testing.T) {
	for _, ref := range []string{"aaaa111", "AAAA1111", "zzzz1111", "", strings.Repeat("a", 65), "-aaaa1111", "////////", "::::::::", "@@@@@@@@", "gggggggg", "aaaa/aaa", "aaaa:aaa", "aaaagaaa"} {
		if err := restore.CheckRef(ref, ""); err == nil {
			t.Errorf("CheckRef(%q) = nil", ref)
		}
	}
}

func TestARefusedRefStatesTheLengthsOfAPrefixAndTheRef(t *testing.T) {
	err := restore.CheckRef("zz", "")
	if err == nil || !strings.Contains(err.Error(), "8-63 lowercase") || !strings.Contains(err.Error(), `"zz"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestLatestWithoutASourceNamesTheFlag(t *testing.T) {
	err := restore.CheckRef("latest", "")
	if err == nil || !strings.Contains(err.Error(), "--source") {
		t.Fatalf("err = %v", err)
	}
}

func snapshotsF() []restore.Snapshot {
	mk := func(prefix string, hour int, tags ...string) restore.Snapshot {
		s := restore.Snapshot{ID: id(prefix), ShortID: prefix[:8], Time: at(hour).UTC(), Tags: tags}
		s.Source = tagOf(tags, "sard.source")
		return s
	}
	return []restore.Snapshot{
		mk("aaaa1111", 3, "sard.source=s1"),
		mk("aaaa2222", 9, "sard.source=s1"),
		mk("bbbb1111", 6, "sard.source=s2", "postgresql.part=database"),
		mk("cccc1111", 7, "sard.source=s2", "postgresql.part=globals"),
	}
}

func tagOf(tags []string, name string) *string {
	for _, t := range tags {
		if v, ok := strings.CutPrefix(t, name+"="); ok {
			return &v
		}
	}
	return nil
}

func TestAPrefixSelectsTheSnapshotByItsFullId(t *testing.T) {
	got, f := restore.Select(snapshotsF(), "aaaa1111", "base", "")
	if f != nil || got.ID != id("aaaa1111") {
		t.Fatalf("got %v, %v", got.ID, f)
	}
}

func TestAFullIdSelectsTheSnapshot(t *testing.T) {
	got, f := restore.Select(snapshotsF(), id("bbbb1111"), "base", "")
	if f != nil || got.ID != id("bbbb1111") {
		t.Fatalf("got %v, %v", got.ID, f)
	}
}

func TestAnAmbiguousPrefixNamesEveryCandidate(t *testing.T) {
	list := snapshotsF()
	list[1].ID = "aaaa1111f" + strings.Repeat("0", 55)
	list[0].ID = "aaaa1111e" + strings.Repeat("0", 55)
	_, f := restore.Select(list, "aaaa1111", "base", "")
	if f == nil || f.Reason != refusal.SnapshotAmbiguous || f.Class != refusal.ClassUsage {
		t.Fatalf("f = %v", f)
	}
	for _, want := range []string{list[0].ID, list[1].ID, "2026-10-10T00:00:00Z", "2026-10-10T06:00:00Z", "s1"} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("%q is not in %q", want, f.Detail)
		}
	}
}

func TestAnUnknownSnapshotPointsToTheList(t *testing.T) {
	_, f := restore.Select(snapshotsF(), "eeee1111", "base", "")
	if f == nil || f.Reason != refusal.SnapshotUnknown || !strings.Contains(f.Detail, "repo snapshots base") {
		t.Fatalf("f = %v", f)
	}
}

func TestLatestIsTheNewestSnapshotOfTheSource(t *testing.T) {
	got, f := restore.Select(snapshotsF(), "latest", "base", "s1")
	if f != nil || got.ID != id("aaaa2222") {
		t.Fatalf("got %v, %v", got.ID, f)
	}
}

func TestLatestSkipsTheGlobalsSnapshotOfPostgreSQL(t *testing.T) {
	got, f := restore.Select(snapshotsF(), "latest", "base", "s2")
	if f != nil || got.ID != id("bbbb1111") {
		t.Fatalf("got %v, %v", got.ID, f)
	}
}

func TestLatestOfASourceWithoutSnapshotsNamesTheSource(t *testing.T) {
	_, f := restore.Select(nil, "latest", "base", "s9")
	if f == nil || f.Reason != refusal.SnapshotUnknown || !strings.Contains(f.Detail, "s9") {
		t.Fatalf("f = %v", f)
	}
}

func TestLatestKeepsTheNewestOfTwoWithTheSameTimeByID(t *testing.T) {
	src := "s"
	list := []restore.Snapshot{
		{ID: id("aa"), Time: time.Unix(10, 0), Source: &src}, {ID: id("bb"), Time: time.Unix(10, 0), Source: &src},
	}
	got, _ := restore.Select(list, "latest", "base", "s")
	if got.ID != id("bb") {
		t.Fatalf("got %v", got.ID)
	}
}

// Р75: with a source the id or the prefix is looked for among the snapshots
// of that source only.
func TestAnIDOfAnotherSourceIsUnknownAndNamesTheSource(t *testing.T) {
	_, f := restore.Select(snapshotsF(), "bbbb1111", "base", "s1")
	if f == nil || f.Reason != refusal.SnapshotUnknown || !strings.Contains(f.Detail, "bbbb1111") || !strings.Contains(f.Detail, "s1") {
		t.Fatalf("f = %v", f)
	}
}

func TestAPrefixFittingSnapshotsOfTwoSourcesIsAmbiguousWithinTheSourceOnly(t *testing.T) {
	list := snapshotsF()
	list[0].ID = "aaaa1111e" + strings.Repeat("0", 55) // s1
	list[2].ID = "aaaa1111f" + strings.Repeat("0", 55) // s2
	got, f := restore.Select(list, "aaaa1111", "base", "s1")
	if f != nil || got.ID != list[0].ID {
		t.Fatalf("got %v, %v", got.ID, f)
	}
}

// A snapshot that carries both postgresql.part tags is what repo restore lays
// out (KindOf takes the first), so latest must not skip it as globals.
func TestLatestAndTheKindOfASnapshotAgreeWhenItCarriesTwoPartTags(t *testing.T) {
	both := restore.Snapshot{ID: id("dddd1111"), ShortID: "dddd1111", Time: at(20).UTC(),
		Tags: []string{"sard.source=s2", "postgresql.part=database", "postgresql.part=globals"}}
	s := "s2"
	both.Source = &s
	got, f := restore.Select(append(snapshotsF(), both), "latest", "base", "s2")
	if f != nil || got.ID != both.ID || restore.KindOf(got) != restore.KindDatabase {
		t.Fatalf("got %v, %v", got.ID, f)
	}
}
