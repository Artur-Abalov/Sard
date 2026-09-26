// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package crap computes the CRAP metric (Change Risk Anti-Patterns) per
// function from cyclomatic complexity and test coverage.
package crap

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strings"
)

// Row is one measured function or method.
type Row struct {
	Name     string
	CC       int
	Coverage float64 // 0..1
}

// Score returns CC^2 * (1 - coverage)^3 + CC.
func Score(cc int, coverage float64) float64 {
	c, u := float64(cc), 1-coverage
	return c*c*u*u*u + c
}

// CRAP returns the row's score.
func (r Row) CRAP() float64 { return Score(r.CC, r.Coverage) }

// Sort orders rows by CRAP descending, ties broken by name.
func Sort(rows []Row) {
	slices.SortStableFunc(rows, func(a, b Row) int {
		return cmp.Or(cmp.Compare(b.CRAP(), a.CRAP()), strings.Compare(a.Name, b.Name))
	})
}

// Offenders returns the rows whose CRAP exceeds threshold. rows must be sorted.
func Offenders(rows []Row, threshold float64) []Row {
	n := 0
	for n < len(rows) && rows[n].CRAP() > threshold {
		n++
	}
	return rows[:n]
}

// Write prints the first top rows as an aligned table. top <= 0 prints all.
func Write(w io.Writer, rows []Row, top int) error {
	if top > 0 {
		rows = rows[:min(top, len(rows))]
	}
	if _, err := fmt.Fprintf(w, "%8s %4s %7s  %s\n", "CRAP", "CC", "COVER", "FUNCTION"); err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := fmt.Fprintf(w, "%8.1f %4d %6.1f%%  %s\n", r.CRAP(), r.CC, r.Coverage*100, r.Name); err != nil {
			return err
		}
	}
	return nil
}
