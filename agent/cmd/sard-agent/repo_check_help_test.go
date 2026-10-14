// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"slices"
	"strings"
	"testing"
)

// Rule "Справка называет флаги, права и коды" (A8d-1: repo check, repo unlock).

func TestTheHelpOfRepoCheckAndRepoUnlockNamesFlagsRightsAndCodes(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		words []string
		wants []string
		codes []int
	}{
		{[]string{"repo", "check"}, []string{"--read-data", "--json", "--timeout", "--connect-timeout", "--config", "sudo", "only reads", "09-troubleshooting.md", "WARN"}, []int{0, 1, 2, 6, 8}},
		{[]string{"repo", "unlock"}, []string{"--remove-stale", "--timeout", "--connect-timeout", "--config", "24", "this host", "never", "STALE_LOCKS_FOREIGN"}, []int{0, 1, 2, 6}},
	} {
		name := strings.Join(c.words, " ")
		h := newSetupHost(t)
		code, stdout, stderr := h.sudo(append(c.words, "--help")...)
		assertCode(t, code, exitOK)
		if stderr != "" {
			t.Fatalf("%s: stderr %q", name, stderr)
		}
		for _, want := range append(c.wants, "Usage: sard-agent "+name) {
			if !strings.Contains(stdout, want) {
				t.Errorf("%s: help lacks %q:\n%s", name, want, stdout)
			}
		}
		var absent []int
		for code := 0; code <= 8; code++ {
			if !slices.Contains(c.codes, code) {
				absent = append(absent, code)
			}
		}
		assertDescribesCodes(t, stdout, c.codes, absent)
	}
}

func TestRepoWithoutASubcommandNamesCheckAndUnlock(t *testing.T) {
	t.Parallel()
	code, _, stderr := runAgent("repo")
	assertCode(t, code, exitUsage)
	for _, want := range []string{"check", "unlock"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q: %q", want, stderr)
		}
	}
}
