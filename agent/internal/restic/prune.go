// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic

import (
	"context"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// PruneSummary is what `restic prune` said on stdout.
type PruneSummary struct {
	// Freed and Size are the "total prune" and "remaining" figures of the
	// summary in bytes; nil when restic printed none or the line is not one
	// this wrapper reads.
	Freed, Size *uint64
	// Lines are the non-empty lines of stdout.
	Lines []string
}

// Prune runs `restic prune` with --retry-lock and no other flag: what is
// removed is restic's decision alone, nothing is repacked on request and no
// snapshot is forgotten. A repository locked by a live process is waited for
// up to lockWait, then the error is ErrLocked.
func (c *CLI) Prune(ctx context.Context) (PruneSummary, error) {
	var sum PruneSummary
	collectLine := func(line []byte) {
		text := string(line)
		if strings.TrimSpace(text) == "" {
			return
		}
		sum.Lines = append(sum.Lines, text)
		if v, ok := summaryBytes(text, "total prune:"); ok {
			sum.Freed = &v
		}
		if v, ok := summaryBytes(text, "remaining:"); ok {
			sum.Size = &v
		}
	}
	_, err := c.runRepo(ctx, call{args: []string{"prune", "--retry-lock", lockWait}, stdout: collectLine})
	return sum, err
}

// sizeLine is the end of a summary line: "<n> blobs / <size> <unit>".
var sizeLine = regexp.MustCompile(`^\s*\d+ blobs / (\d+(?:\.\d+)?) (B|KiB|MiB|GiB|TiB)\s*$`)

var units = map[string]float64{"B": 1, "KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30, "TiB": 1 << 40}

// summaryBytes reads the size of a summary line that starts with label.
func summaryBytes(line, label string) (uint64, bool) {
	rest, ok := strings.CutPrefix(line, label)
	if !ok {
		return 0, false
	}
	m := sizeLine.FindStringSubmatch(rest)
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	return uint64(math.Round(n * units[m[2]])), true
}
