// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"strings"
	"testing"
)

// Rule "Справка есть у каждой команды".

func TestHelpDescribesFlagsPrivilegesAndExitCodes(t *testing.T) {
	cases := []struct {
		words []string
		flags []string
		sudo  string
		codes []int
	}{
		{[]string{"secret", "set"}, []string{"--stdin", "--from-file", "--no-restart", "--config"}, "sudo", []int{0, 1, 2, 6, 7}},
		{[]string{"secret", "list"}, []string{"--json", "--config"}, "service user", []int{0, 2}},
		{[]string{"secret", "remove"}, []string{"--no-restart", "--config"}, "sudo", []int{0, 1, 2, 6, 7}},
		{[]string{"repo", "show"}, []string{"--json", "--timeout", "--config"}, "service user", []int{0, 1, 2, 6}},
		{[]string{"repo", "remove"}, []string{"--no-restart", "--config"}, "sudo", []int{0, 1, 2, 6, 7}},
		{[]string{"repo", "password"}, []string{"--reveal", "--config"}, "service user", []int{0, 2}},
		{[]string{"repo", "list"}, []string{"--json", "--timeout", "--config"}, "service user", []int{0, 1, 2, 6}},
		{[]string{"repo", "init"}, []string{"--generate-password", "--timeout", "--config"}, "sudo", []int{0, 1, 2, 4, 6, 7}},
		{[]string{"repo", "add"}, []string{"--password-stdin", "--password-from-file", "--no-restart", "--timeout", "--config"}, "sudo", []int{0, 1, 2, 4, 6, 7}},
	}
	for _, c := range cases {
		name := strings.Join(c.words, " ")
		t.Run(name, func(t *testing.T) {
			h := newSetupHost(t)
			code, stdout, stderr := h.sudo(append(c.words, "--help")...)
			assertCode(t, code, exitOK)
			if stderr != "" {
				t.Fatalf("stderr %q", stderr)
			}
			for _, want := range append(c.flags, c.sudo, "Usage: sard-agent "+name) {
				if !strings.Contains(stdout, want) {
					t.Errorf("help lacks %q:\n%s", want, stdout)
				}
			}
			assertDescribesCodes(t, stdout, c.codes, otherCodes(c.codes))
			if strings.Contains(stdout, "sudo -u") {
				t.Error("help mentions sudo -u")
			}
		})
	}
}

func otherCodes(have []int) []int {
	var out []int
	for code := 0; code <= 7; code++ {
		found := false
		for _, h := range have {
			found = found || h == code
		}
		if !found {
			out = append(out, code)
		}
	}
	return out
}

func TestRepoAddHelpDescribesTheAddressTheApplyingAndTheSudo(t *testing.T) {
	h := newSetupHost(t)
	code, stdout, _ := h.sudo("repo", "add", "--help")
	assertCode(t, code, exitOK)
	for _, want := range []string{"only a local path", "through sudo", "sard-agent.service", "a step is running", "D13"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help lacks %q:\n%s", want, stdout)
		}
	}
}

func TestSecretWithoutASubcommandNamesThem(t *testing.T) {
	h := newSetupHost(t)
	code, stdout, stderr := h.sudo("secret")
	assertCode(t, code, exitUsage)
	if stdout != "" {
		t.Fatalf("stdout %q", stdout)
	}
	for _, want := range []string{"set", "list", "remove"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q: %q", want, stderr)
		}
	}
}

func TestRepoWithoutASubcommandNamesAllOfThem(t *testing.T) {
	code, _, stderr := runAgent("repo")
	assertCode(t, code, exitUsage)
	for _, want := range []string{"init", "list", "add", "show", "remove", "password"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q: %q", want, stderr)
		}
	}
}

func TestSecretIsDispatchedFromTheAgentCommandLine(t *testing.T) {
	code, stdout, _ := runAgent("secret", "set", "--help")
	assertCode(t, code, exitOK)
	if !strings.Contains(stdout, "Usage: sard-agent secret set") {
		t.Fatalf("stdout %q", stdout)
	}
	code, _, stderr := runAgent("secret")
	assertCode(t, code, exitUsage)
	if !strings.Contains(stderr, "sard-agent secret: want a subcommand") {
		t.Fatalf("stderr %q", stderr)
	}
}
