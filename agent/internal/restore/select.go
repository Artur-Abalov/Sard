// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restore

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
)

// Latest is the word that stands for the newest snapshot of a source (Р58).
const Latest = "latest"

// The lengths of an id: the full id and the shortest prefix repo restore takes.
const (
	fullIDLength   = 64
	minPrefixChars = 8
)

// globalsTag marks the snapshot of the global objects of PostgreSQL, which is
// written after the snapshot of its database (Р58).
const globalsTag = "postgresql.part=globals"

// CheckRef: the snapshot argument of repo restore is a full id, a prefix of
// 8-63 lowercase hexadecimal characters or "latest", which needs a source.
func CheckRef(ref, source string) error {
	if ref == Latest {
		return checkLatest(source)
	}
	if !isIDPrefix(ref) {
		return fmt.Errorf("the snapshot must be a full id, a prefix of %d-%d lowercase hexadecimal characters or %s, got %q", minPrefixChars, fullIDLength-1, Latest, ref)
	}
	return nil
}

func checkLatest(source string) error {
	if source == "" {
		return errors.New("latest needs --source: one repository holds the snapshots of many sources")
	}
	return nil
}

func isIDPrefix(ref string) bool {
	return len(ref) >= minPrefixChars && len(ref) <= fullIDLength && !strings.ContainsFunc(ref, notLowerHex)
}

func notLowerHex(r rune) bool { return (r < '0' || r > '9') && (r < 'a' || r > 'f') }

// Select is the snapshot ref names among list, which holds the snapshots of
// the source asked for (all of them without one): by a full id, by a prefix
// of an id or, for latest, the newest of them except a snapshot of global
// objects of PostgreSQL.
func Select(list []Snapshot, ref, repoName, source string) (Snapshot, *refusal.Failure) {
	if ref == Latest {
		return newest(list, source)
	}
	var found []Snapshot
	for _, s := range list {
		if strings.HasPrefix(s.ID, ref) {
			found = append(found, s)
		}
	}
	switch len(found) {
	case 0:
		return Snapshot{}, refusal.Fail(refusal.SnapshotUnknown, "no snapshot %s in repository %s; see the snapshots with `sard-agent repo snapshots %s`", ref, repoName, repoName)
	case 1:
		return found[0], nil
	}
	return Snapshot{}, ambiguous(found, ref)
}

func newest(list []Snapshot, source string) (Snapshot, *refusal.Failure) {
	candidates := slices.DeleteFunc(slices.Clone(list), func(s Snapshot) bool {
		return !ofSource(s, source) || slices.Contains(s.Tags, globalsTag)
	})
	if len(candidates) == 0 {
		return Snapshot{}, refusal.Fail(refusal.SnapshotUnknown, "no snapshot of source %s in the repository", source)
	}
	return slices.MaxFunc(candidates, func(a, b Snapshot) int {
		return cmp.Or(a.Time.Compare(b.Time), strings.Compare(a.ID, b.ID))
	}), nil
}

func ofSource(s Snapshot, source string) bool { return s.Source != nil && *s.Source == source }

func ambiguous(found []Snapshot, ref string) *refusal.Failure {
	var lines []string
	for _, s := range found {
		source := "-"
		if s.Source != nil {
			source = *s.Source
		}
		lines = append(lines, fmt.Sprintf("%s (%s, source %s)", s.ID, s.Stamp(), source))
	}
	return refusal.Fail(refusal.SnapshotAmbiguous, "the prefix %s fits %d snapshots: %s; give more characters", ref, len(found), strings.Join(lines, ", "))
}
