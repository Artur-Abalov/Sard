// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

func itemsOf(n int) []restic.ItemError {
	var items []restic.ItemError
	for i := 1; i <= n; i++ {
		p := fmt.Sprintf("/d/f%d", i)
		items = append(items, restic.ItemError{Item: p, During: "scan"}, restic.ItemError{Item: p, During: "archival"})
	}
	return items
}

func TestUnreadableCountsDistinctPathsAndNamesTheFirstTenInOrder(t *testing.T) {
	count, lines := (&restic.PartialError{Items: itemsOf(11)}).Unreadable()
	want := []string{"/d/f1", "/d/f2", "/d/f3", "/d/f4", "/d/f5", "/d/f6", "/d/f7", "/d/f8", "/d/f9", "/d/f10"}
	if count != 11 || !slices.Equal(lines, want) {
		t.Errorf("count %d, lines %q", count, lines)
	}
}

func TestUnreadableWithExactlyTenPathsNamesAllOfThem(t *testing.T) {
	count, lines := (&restic.PartialError{Items: itemsOf(10)}).Unreadable()
	if count != 10 || len(lines) != 10 || lines[9] != "/d/f10" {
		t.Errorf("count %d, lines %q", count, lines)
	}
}

func TestUnreadableWritesEachPathOnOneLineQuotedAsInTheErrorText(t *testing.T) {
	items := []restic.ItemError{{Item: "/d/a\nb"}, {Item: `/d/q"t`}, {Item: "x"}}
	count, lines := (&restic.PartialError{Items: items}).Unreadable()
	want := []string{`/d/a\nb`, `/d/q\"t`, "x"}
	if count != 3 || !slices.Equal(lines, want) {
		t.Errorf("count %d, lines %q", count, lines)
	}
}

func TestUnreadableOfNoItemsIsEmpty(t *testing.T) {
	count, lines := (&restic.PartialError{}).Unreadable()
	if count != 0 || len(lines) != 0 {
		t.Errorf("count %d, lines %q", count, lines)
	}
}
