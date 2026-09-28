// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/enroll"
)

func runEnrollCmdTest(args ...string) (int, string, string) {
	return runEnrollCmdOn(fixedHostname, args...)
}

func runEnrollCmdOn(hostname hostnameFunc, args ...string) (int, string, string) {
	var out, errOut strings.Builder
	all := append([]string{"enroll"}, args...)
	code := run(context.Background(), all, &out, &errOut, hostname)
	return code, out.String(), errOut.String()
}

// Справка перечисляет флаги, способы передачи токена и коды выхода
func TestHelpListsFlagsTokenSourcesAndExitCodes(t *testing.T) {
	code, out, errOut := runEnrollCmdTest("--help")
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if errOut != "" {
		t.Fatalf("stderr = %q, want empty", errOut)
	}
	requireAllContained(t, out, "--server", "--token", "--token-file", "--force", "--config", "--timeout", "SARD_ENROLL_TOKEN")
	for _, c := range enrollHelpCodes() {
		want := fmt.Sprintf("  %d  %s", c.code, c.word)
		if !strings.Contains(out, want) {
			t.Errorf("help does not print %q:\n%s", want, out)
		}
	}
	requireNoneContained(t, strings.ToLower(out), "insecure", "skip", "disable")
}

// F8: enrollClassCodes must name every enroll.Class exactly once, at the
// exit code В3 assigns it, and --help must print each of the eight codes
// with the word this table gives it — not merely contain the digit
// somewhere ("30s" would satisfy a bare "contains '3'" check).
func TestEveryEnrollClassHasAnExitCodeAndTheHelpPrintsIt(t *testing.T) {
	requireEveryClassHasItsCode(t, map[enroll.Class]int{
		enroll.ClassAgentError:   1,
		enroll.ClassUsage:        2,
		enroll.ClassTokenRefused: 3,
		enroll.ClassTrust:        5,
		enroll.ClassTemporary:    6,
		enroll.ClassWrite:        7,
	})
	_, out, _ := runEnrollCmdTest("--help")
	requireHelpPrintsEveryCode(t, out)
}

func requireEveryClassHasItsCode(t *testing.T, want map[enroll.Class]int) {
	t.Helper()
	if len(enrollClassCodes) != len(want) {
		t.Fatalf("enrollClassCodes has %d rows, want %d (one per enroll.Class)", len(enrollClassCodes), len(want))
	}
	seen := map[enroll.Class]bool{}
	for _, c := range enrollClassCodes {
		if seen[c.class] {
			t.Errorf("class %q appears more than once in enrollClassCodes", c.class)
		}
		seen[c.class] = true
		requireClassCode(t, c, want[c.class])
	}
}

func requireClassCode(t *testing.T, c enrollClassCode, wantCode int) {
	t.Helper()
	if c.code != wantCode {
		t.Errorf("enrollClassCodes: class %q -> code %d, want %d", c.class, c.code, wantCode)
	}
	if got := enrollExitCode(c.class); got != wantCode {
		t.Errorf("enrollExitCode(%q) = %d, want %d", c.class, got, wantCode)
	}
}

func requireHelpPrintsEveryCode(t *testing.T, out string) {
	t.Helper()
	for _, c := range enrollHelpCodes() {
		line := fmt.Sprintf("  %d  %s", c.code, c.word)
		if !strings.Contains(out, line) {
			t.Errorf("help does not print %q:\n%s", line, out)
		}
	}
}

func requireAllContained(t *testing.T, haystack string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(haystack, want) {
			t.Errorf("does not mention %q:\n%s", want, haystack)
		}
	}
}

func requireNoneContained(t *testing.T, haystack string, forbidden ...string) {
	t.Helper()
	for _, f := range forbidden {
		if strings.Contains(haystack, f) {
			t.Errorf("must not offer a way to bypass server verification (found %q):\n%s", f, haystack)
		}
	}
}
