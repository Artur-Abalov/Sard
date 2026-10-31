// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package retention

import (
	"slices"
	"strings"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// Task is what the server asks of a FORGET step (proto ForgetTask).
type Task struct {
	// Source is the value of the tag sard.source; only its snapshots count.
	Source string
	Policy Policy
	// Keep are snapshots kept on top of the policy; it only adds protection.
	Keep []string
	// Incomplete are the snapshots the server knows to be incomplete (КL6).
	Incomplete       []string
	IncompleteMaxAge time.Duration
}

// Decision is what a FORGET step does with the snapshots of a source.
type Decision struct {
	// Parts are the parts to forget and Mains the main snapshots, each the
	// oldest first. The parts go first (КL5): an interrupted step may leave a
	// main snapshot without its part, never a part without its main one.
	Parts, Mains []string
	// KeptByFloor are the main snapshots the policy would forget and the
	// floor of the host kept.
	KeptByFloor []string
}

// SourceTag is the tag that says which source a snapshot belongs to.
const SourceTag = "sard.source="

// mainSnapshotSuffix ends the key of the tag that makes a snapshot the part
// of another one: "<plugin>.main_snapshot=<full id of the main snapshot>" (V2).
const mainSnapshotSuffix = "." + sdk.MainSnapshotTag

// mainOf is the id of the main snapshot s is a part of; false for a main one.
func mainOf(s restic.Snapshot) (string, bool) {
	for _, tag := range s.Tags {
		if key, id, ok := strings.Cut(tag, "="); ok && id != "" && strings.HasSuffix(key, mainSnapshotSuffix) {
			return id, true
		}
	}
	return "", false
}

// Plan decides which snapshots of the source the step forgets. Only
// snapshots tagged sard.source=<source> are looked at, whatever host made
// them. The floor of this host is applied last and only ever removes ids from
// the decision of the policy (КL5-КL8).
func Plan(snaps []restic.Snapshot, t Task, floor config.Floor, now time.Time) Decision {
	mains, parts := split(snaps, t.Source)
	candidates := t.forgettable(mains, now)
	protected := floorKeep(mains, floor, now)
	var d Decision
	gone := map[string]bool{}
	for _, s := range newestFirst(mains) {
		switch {
		case !candidates[s.ID]:
		case protected[s.ID]:
			d.KeptByFloor = append(d.KeptByFloor, s.ID)
		default:
			gone[s.ID] = true
		}
	}
	d.Parts, d.Mains = forgottenParts(parts, mains, gone), forgottenIDs(mains, gone)
	return d
}

// Forget is every id the step forgets: the parts, then the main snapshots.
func (d Decision) Forget() []string { return slices.Concat(d.Parts, d.Mains) }

// split sorts the snapshots of the source into main snapshots and parts;
// the snapshots of other sources, and those of none, are not looked at.
func split(snaps []restic.Snapshot, source string) (mains, parts []restic.Snapshot) {
	for _, s := range snaps {
		if !slices.Contains(s.Tags, SourceTag+source) {
			continue
		}
		if _, isPart := mainOf(s); isPart {
			parts = append(parts, s)
		} else {
			mains = append(mains, s)
		}
	}
	return mains, parts
}

// forgettable are the main snapshots the policy and the hint would forget,
// before the floor: complete ones that the policy does not keep and
// incomplete ones that are old enough with a complete one newer.
func (t Task) forgettable(mains []restic.Snapshot, now time.Time) map[string]bool {
	complete, partial := t.byCompleteness(mains)
	kept := t.Policy.keep(complete, now)
	for _, id := range t.Keep {
		kept[id] = true
	}
	out := map[string]bool{}
	for _, s := range complete {
		out[s.ID] = !kept[s.ID]
	}
	for _, s := range partial {
		out[s.ID] = t.stale(s, complete, now) && !kept[s.ID]
	}
	return out
}

// byCompleteness splits main snapshots by the hint of the server.
func (t Task) byCompleteness(mains []restic.Snapshot) (complete, partial []restic.Snapshot) {
	for _, s := range mains {
		if slices.Contains(t.Incomplete, s.ID) {
			partial = append(partial, s)
		} else {
			complete = append(complete, s)
		}
	}
	return complete, partial
}

// stale: an incomplete snapshot older than IncompleteMaxAge with a complete
// snapshot newer than it.
func (t Task) stale(s restic.Snapshot, complete []restic.Snapshot, now time.Time) bool {
	if t.IncompleteMaxAge <= 0 || now.Sub(s.Time) <= t.IncompleteMaxAge {
		return false
	}
	return slices.ContainsFunc(complete, func(c restic.Snapshot) bool { return c.Time.After(s.Time) })
}

// floorKeep are the main snapshots the floor of the host protects: the
// latest KeepLast and those not older than KeepWithinDays from the newest
// (or from now, if the newest is dated in the future).
func floorKeep(mains []restic.Snapshot, f config.Floor, now time.Time) map[string]bool {
	if f.Disabled {
		return nil
	}
	return Policy{Last: f.KeepLast, Within: time.Duration(f.KeepWithinDays) * 24 * time.Hour}.keep(mains, now)
}

// forgottenParts are the parts of forgotten main snapshots and those whose
// main snapshot is not in the repository (КL5).
func forgottenParts(parts, mains []restic.Snapshot, gone map[string]bool) []string {
	exists := map[string]bool{}
	for _, m := range mains {
		exists[m.ID] = true
	}
	var out []string
	for _, p := range newestFirst(parts) {
		if main, _ := mainOf(p); gone[main] || !exists[main] {
			out = append(out, p.ID)
		}
	}
	slices.Reverse(out)
	return out
}

func forgottenIDs(mains []restic.Snapshot, gone map[string]bool) []string {
	var out []string
	for _, m := range newestFirst(mains) {
		if gone[m.ID] {
			out = append(out, m.ID)
		}
	}
	slices.Reverse(out)
	return out
}
