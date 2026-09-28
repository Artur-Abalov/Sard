// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"strings"
	"testing"
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
	requireAllContained(t, out, "0", "1", "2", "3", "4", "5", "6", "7")
	requireNoneContained(t, strings.ToLower(out), "insecure", "skip", "disable")
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
