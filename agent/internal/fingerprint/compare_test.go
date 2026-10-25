// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package fingerprint_test

import (
	"slices"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/fingerprint"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

func fp(plugin string, facts ...sdk.Fact) sdk.Fingerprint {
	return sdk.Fingerprint{Plugin: plugin, Format: sdk.FingerprintFormat, Facts: facts}
}

func exact(name, value string) sdk.Fact {
	return sdk.Fact{Name: name, Rule: sdk.RuleExact, Value: value}
}
func info(name, value string) sdk.Fact { return sdk.Fact{Name: name, Rule: sdk.RuleInfo, Value: value} }

func texts(r fingerprint.Result) []string {
	var out []string
	for _, m := range r.Mismatches {
		out = append(out, m.Text())
	}
	return out
}

func TestTheSameFingerprintMatches(t *testing.T) {
	src := fp("postgresql", exact("tables", "3"), exact("rows:public.users", "10"), info("database_size", "8192"))
	copied := fp("postgresql", exact("rows:public.users", "10"), exact("tables", "3"), info("database_size", "9000"))
	r := fingerprint.Compare(src, copied)
	if !r.Matches() || len(r.Mismatches) != 0 || len(r.Unverified) != 0 {
		t.Errorf("Compare = %+v", r)
	}
}

// A row deleted from the copy is a mismatch that names the table.
func TestADifferentValueIsAMismatchInWords(t *testing.T) {
	src := fp("postgresql", exact("rows:public.users", "10"), exact("tables", "3"))
	copied := fp("postgresql", exact("rows:public.users", "9"), exact("tables", "3"))
	r := fingerprint.Compare(src, copied)
	want := []fingerprint.Mismatch{{Name: "rows:public.users", Expected: "10", Actual: "9"}}
	if r.Matches() || !slices.Equal(r.Mismatches, want) {
		t.Fatalf("Compare = %+v", r)
	}
	if got := texts(r); !slices.Equal(got, []string{"rows:public.users: 10 in the source, 9 in the copy"}) {
		t.Errorf("texts = %q", got)
	}
}

func TestAFactAbsentFromTheCopyIsAMismatch(t *testing.T) {
	r := fingerprint.Compare(fp("files", exact("sha256:/a", "ab")), fp("files"))
	want := []fingerprint.Mismatch{{Name: "sha256:/a", Expected: "ab", Missing: true}}
	if !slices.Equal(r.Mismatches, want) {
		t.Fatalf("Compare = %+v", r)
	}
	if got := texts(r); !slices.Equal(got, []string{"sha256:/a: ab in the source, absent from the copy"}) {
		t.Errorf("texts = %q", got)
	}
}

// An empty value is shown as such, not as nothing.
func TestAnEmptyValueIsNamed(t *testing.T) {
	r := fingerprint.Compare(fp("files", exact("a", "")), fp("files", exact("a", "x")))
	if got := texts(r); !slices.Equal(got, []string{`a: "" in the source, x in the copy`}) {
		t.Errorf("texts = %q", got)
	}
}

// What the source holds for information only is never compared; what the
// copy could only measure for information is not verified, which is no match.
func TestInformationIsNotComparedAndAnInexactCopyIsUnverified(t *testing.T) {
	src := fp("postgresql", info("database_size", "1"), exact("rows:a", "5"), exact("rows:b", "6"))
	copied := fp("postgresql", info("rows:a", "~5"), exact("rows:b", "6"))
	r := fingerprint.Compare(src, copied)
	if r.Matches() || len(r.Mismatches) != 0 || !slices.Equal(r.Unverified, []string{"rows:a"}) {
		t.Errorf("Compare = %+v", r)
	}
}

// Facts of the copy the source does not have are not judged: the copy is
// measured by the facts of the source.
func TestExtraFactsOfTheCopyAreIgnored(t *testing.T) {
	r := fingerprint.Compare(fp("files", exact("a", "1")), fp("files", exact("a", "1"), exact("b", "2")))
	if !r.Matches() {
		t.Errorf("Compare = %+v", r)
	}
}

func TestFingerprintsOfDifferentPluginsOrFormatsDoNotMatch(t *testing.T) {
	r := fingerprint.Compare(fp("files"), fp("postgresql"))
	if want := []fingerprint.Mismatch{{Name: "plugin", Expected: "files", Actual: "postgresql"}}; !slices.Equal(r.Mismatches, want) {
		t.Errorf("plugin: %+v", r)
	}
	other := fp("files")
	other.Format = 2
	r = fingerprint.Compare(fp("files"), other)
	if want := []fingerprint.Mismatch{{Name: "format", Expected: "1", Actual: "2"}}; !slices.Equal(r.Mismatches, want) {
		t.Errorf("format: %+v", r)
	}
}

// The mismatches come in the order of the names, whatever the order of the facts.
func TestMismatchesAreSortedByName(t *testing.T) {
	src := fp("files", exact("b", "1"), exact("a", "1"), exact("c", "1"))
	copied := fp("files", exact("c", "2"), exact("a", "2"))
	r := fingerprint.Compare(src, copied)
	var names []string
	for _, m := range r.Mismatches {
		names = append(names, m.Name)
	}
	if !slices.Equal(names, []string{"a", "b", "c"}) {
		t.Errorf("names = %q", names)
	}
	r = fingerprint.Compare(fp("files", exact("b", "1"), exact("a", "1")), fp("files", info("a", ""), info("b", "")))
	if !slices.Equal(r.Unverified, []string{"a", "b"}) {
		t.Errorf("unverified = %q", r.Unverified)
	}
}
