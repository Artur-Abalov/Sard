// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package retention decides which snapshots a FORGET step forgets and carries
// the step out (docs/specs/agent/retention-forget.feature, L1a). The server
// sends a policy; the agent computes the snapshots itself, never goes below
// the retention floor of its host and calls restic forget with snapshot ids
// only.
package retention

import (
	"slices"
	"strings"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// Policy is the rules of restic forget (КL7); a snapshot kept by any rule
// stays.
type Policy struct {
	Last, Daily, Weekly, Monthly, Yearly uint32
	Within                               time.Duration
}

// Empty reports a policy that keeps nothing: restic itself refuses it.
func (p Policy) Empty() bool { return p == Policy{} }

// newestFirst sorts snapshots by time, the newest first; equal times by id.
func newestFirst(snaps []restic.Snapshot) []restic.Snapshot {
	sorted := slices.Clone(snaps)
	slices.SortStableFunc(sorted, func(a, b restic.Snapshot) int {
		if c := b.Time.Compare(a.Time); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return sorted
}

// bucket is the rule "keep the newest snapshot of each of the last Count
// periods", as restic counts them.
type bucket struct {
	count uint32
	of    func(t time.Time) int
	last  int
}

func buckets(p Policy) []*bucket {
	return []*bucket{
		{count: p.Daily, of: func(t time.Time) int { return t.Year()*10000 + int(t.Month())*100 + t.Day() }},
		{count: p.Weekly, of: func(t time.Time) int { y, w := t.ISOWeek(); return y*100 + w }},
		{count: p.Monthly, of: func(t time.Time) int { return t.Year()*100 + int(t.Month()) }},
		{count: p.Yearly, of: func(t time.Time) int { return t.Year() }},
	}
}

// keep is the set of ids restic forget with the policy keeps among snapshots,
// as one group: the rules of restic's ApplyPolicy over all of them. Like
// restic, it measures Within from the newest snapshot, but never from one
// later than now: a snapshot dated in the future (a skewed clock, a forged
// time) cannot push the boundary of Within past what really is recent.
func (p Policy) keep(snaps []restic.Snapshot, now time.Time) map[string]bool {
	kept := map[string]bool{}
	list := newestFirst(snaps)
	bs := buckets(p)
	for i, s := range list {
		if i < int(min(p.Last, uint32(len(list)))) || p.within(minTime(list[0].Time, now), s) {
			kept[s.ID] = true
		}
		for _, b := range bs {
			if b.take(s) {
				kept[s.ID] = true
			}
		}
	}
	return kept
}

// within: s is newer than Within before the reference time.
func (p Policy) within(reference time.Time, s restic.Snapshot) bool {
	return p.Within != 0 && s.Time.After(reference.Add(-p.Within))
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

// take reports whether s is the first snapshot of a new period that the rule
// still counts.
func (b *bucket) take(s restic.Snapshot) bool {
	if b.count == 0 {
		return false
	}
	v := b.of(s.Time)
	if v == b.last {
		return false
	}
	b.last = v
	b.count--
	return true
}
