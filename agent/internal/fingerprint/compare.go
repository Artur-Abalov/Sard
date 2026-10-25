// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package fingerprint compares the fingerprint of a source with that of a
// restored copy (V-D3): the plugin measures, the agent judges, so that no
// plugin writes its own rules of a check.
package fingerprint

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// Mismatch is a fact of the source the copy does not have as it was.
type Mismatch struct {
	Name     string
	Expected string
	// Actual is the value in the copy; Missing: the copy has no such fact.
	Actual  string
	Missing bool
}

// Text is the mismatch in words: "rows:public.users: 10 in the source, 9 in the copy".
func (m Mismatch) Text() string {
	actual := shown(m.Actual) + " in the copy"
	if m.Missing {
		actual = "absent from the copy"
	}
	return fmt.Sprintf("%s: %s in the source, %s", m.Name, shown(m.Expected), actual)
}

func shown(v string) string {
	if v == "" {
		return `""`
	}
	return v
}

// Result is the outcome of a comparison.
type Result struct {
	// Mismatches are sorted by name.
	Mismatches []Mismatch
	// Unverified names, sorted, the exact facts of the source that the copy
	// could only measure for information (a count that took too long).
	Unverified []string
}

// Matches: no mismatch and every exact fact verified.
func (r Result) Matches() bool { return len(r.Mismatches) == 0 && len(r.Unverified) == 0 }

// judge compares one exact fact of the source with the copy.
func (r *Result) judge(want sdk.Fact, copied sdk.Fingerprint) {
	got, ok := copied.Lookup(want.Name)
	switch {
	case !ok:
		r.Mismatches = append(r.Mismatches, Mismatch{Name: want.Name, Expected: want.Value, Missing: true})
	case got.Rule != sdk.RuleExact:
		r.Unverified = append(r.Unverified, want.Name)
	case got.Value != want.Value:
		r.Mismatches = append(r.Mismatches, Mismatch{Name: want.Name, Expected: want.Value, Actual: got.Value})
	}
}

// Compare judges the copy by the facts of the source: each exact fact of the
// source must be in the copy with the same value; facts for information and
// the facts only the copy has are not judged.
func Compare(source, copied sdk.Fingerprint) Result {
	var r Result
	if source.Plugin != copied.Plugin {
		r.Mismatches = append(r.Mismatches, Mismatch{Name: "plugin", Expected: source.Plugin, Actual: copied.Plugin})
	}
	if source.Format != copied.Format {
		r.Mismatches = append(r.Mismatches, Mismatch{Name: "format", Expected: strconv.Itoa(source.Format), Actual: strconv.Itoa(copied.Format)})
	}
	for _, want := range source.Facts {
		if want.Rule == sdk.RuleExact {
			r.judge(want, copied)
		}
	}
	slices.SortStableFunc(r.Mismatches, func(a, b Mismatch) int { return cmp.Compare(a.Name, b.Name) })
	slices.Sort(r.Unverified)
	return r
}
