// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restore_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/restore"
)

type fakeLister struct {
	snapshots []restic.Snapshot
	err       error
	tags      [][]string
}

func (f *fakeLister) Snapshots(_ context.Context, tags []string) ([]restic.Snapshot, error) {
	f.tags = append(f.tags, tags)
	return f.snapshots, f.err
}

func id(prefix string) string { return prefix + strings.Repeat("0", 64-len(prefix)) }

func at(hour int) time.Time {
	return time.Date(2026, 10, 10, hour, 0, 0, 0, time.FixedZone("MSK", 3*3600))
}

func TestSnapshotsAreOrderedByTimeThenIDAndShownInUTC(t *testing.T) {
	l := &fakeLister{snapshots: []restic.Snapshot{
		{ID: id("bb"), Time: at(6)}, {ID: id("cc"), Time: at(9)}, {ID: id("aa"), Time: at(6)}, {ID: id("dd"), Time: at(3)},
	}}
	got, err := restore.List(context.Background(), l, "")
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, s := range got {
		order = append(order, s.ShortID)
	}
	if !slices.Equal(order, []string{"dd000000", "aa000000", "bb000000", "cc000000"}) {
		t.Fatalf("order %v", order)
	}
	if got[0].Stamp() != "2026-10-10T00:00:00Z" || got[0].Time.Location() != time.UTC {
		t.Errorf("time %s in %v", got[0].Stamp(), got[0].Time.Location())
	}
	if len(l.tags) != 1 || len(l.tags[0]) != 0 {
		t.Errorf("tags asked for: %v", l.tags)
	}
}

func TestSourceRunAndStepComeFromTheSardTags(t *testing.T) {
	l := &fakeLister{snapshots: []restic.Snapshot{
		{ID: id("aa"), Tags: []string{"sard.run=r1", "sard.source=s1", "sard.step=st1", "other"}},
		{ID: id("bb"), Tags: []string{"sard.run=r9", "sard.source=", "sard.step="}},
		{ID: id("cc")},
	}}
	got, _ := restore.List(context.Background(), l, "")
	text := func(p *string) string {
		if p == nil {
			return "<nil>"
		}
		return *p
	}
	for i, want := range [][3]string{{"s1", "r1", "st1"}, {"<nil>", "r9", "<nil>"}, {"<nil>", "<nil>", "<nil>"}} {
		if g := [3]string{text(got[i].Source), text(got[i].Run), text(got[i].Step)}; g != want {
			t.Errorf("snapshot %d: %v, want %v", i, g, want)
		}
	}
	if !slices.Equal(got[0].Tags, []string{"sard.run=r1", "sard.source=s1", "sard.step=st1", "other"}) {
		t.Errorf("tags %v", got[0].Tags)
	}
}

func TestASourceFiltersByTheSardSourceTag(t *testing.T) {
	l := &fakeLister{}
	if _, err := restore.List(context.Background(), l, "s2"); err != nil {
		t.Fatal(err)
	}
	if len(l.tags) != 1 || !slices.Equal(l.tags[0], []string{"sard.source=s2"}) {
		t.Fatalf("tags %v", l.tags)
	}
}

func TestListPassesTheErrorOfRestic(t *testing.T) {
	l := &fakeLister{err: restic.ErrWrongPassword}
	if _, err := restore.List(context.Background(), l, ""); !errors.Is(err, restic.ErrWrongPassword) {
		t.Fatalf("err %v", err)
	}
}

func TestCheckSource(t *testing.T) {
	for _, c := range []struct {
		source string
		ok     bool
	}{
		{"s1", true}, {"a.b-c_d:1", true}, {strings.Repeat("x", 128), true},
		{"", false}, {"a,b", false}, {"a b", false}, {strings.Repeat("x", 129), false},
		{"tab\t", false}, {"é", false}, {"del\x7f", false},
	} {
		err := restore.CheckSource(c.source)
		if (err == nil) != c.ok {
			t.Errorf("%q: %v", c.source, err)
		}
		if err != nil && !strings.Contains(err.Error(), "--source") {
			t.Errorf("%q: the message does not name the flag: %v", c.source, err)
		}
	}
}

func TestAMissingRepositoryIsNotInitialized(t *testing.T) {
	target := repoinit.Target{Name: "base", Backend: "local", Scrub: func(s string) string { return s }}
	f := restore.ListFailure(context.Background(), restic.ErrNoRepository, target)
	if f.Reason != refusal.RepositoryNotInitialized || f.Class != refusal.ClassUsage {
		t.Fatalf("failure %+v", f)
	}
}

func TestOtherFailuresAreExplainedAsForTheOtherRepoCommands(t *testing.T) {
	target := repoinit.Target{Name: "base", Backend: "local", Scrub: func(s string) string { return s }}
	f := restore.ListFailure(context.Background(), restic.ErrWrongPassword, target)
	if f.Reason != refusal.WrongPassword {
		t.Fatalf("failure %+v", f)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if f := restore.ListFailure(ctx, restic.ErrNoRepository, target); f.Reason != refusal.Interrupted {
		t.Fatalf("a cancelled command: %+v", f)
	}
}
