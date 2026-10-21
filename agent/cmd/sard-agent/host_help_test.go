// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"strings"
	"testing"
)

// Rule "Справка есть у каждой команды".

func TestHelpDescribesFlagsPrivilegesAndExitCodes(t *testing.T) {
	t.Parallel()
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
		{[]string{"repo", "init"}, []string{"--generate-password", "--recovery-key-file", "--no-recovery-key", "--timeout", "--config"}, "sudo", []int{0, 1, 2, 4, 6, 7}},
		{[]string{"repo", "add"}, []string{"--password-stdin", "--password-from-file", "--recovery-key-file", "--no-recovery-key", "--no-restart", "--timeout", "--connect-timeout", "--config"}, "sudo", []int{0, 1, 2, 4, 5, 6, 7}},
		{[]string{"repo", "key", "add"}, []string{"--for", "--label", "--to-file", "--if-missing", "--no-restart", "--timeout", "--connect-timeout", "--config"}, "sudo", []int{0, 1, 2, 6, 7}},
		{[]string{"repo", "key", "list"}, []string{"--json", "--timeout", "--connect-timeout", "--config"}, "service user", []int{0, 1, 2, 6}},
		{[]string{"repo", "key", "remove"}, []string{"--yes", "--no-restart", "--timeout", "--connect-timeout", "--config"}, "sudo", []int{0, 1, 2, 6, 7}},
	}
	for _, c := range cases {
		name := strings.Join(c.words, " ")
		t.Run(name, func(t *testing.T) {
			t.Parallel()
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
			if strings.Contains(stdout, "sudo -u") { // allow-sudo-u: absence guard, the help must not tell to use it
				t.Error("help mentions sudo -u") // allow-sudo-u: absence guard, the help must not tell to use it
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
	t.Parallel()
	h := newSetupHost(t)
	code, stdout, _ := h.sudo("repo", "add", "--help")
	assertCode(t, code, exitOK)
	for _, want := range []string{"a local path (an absolute path", "through sudo", "symbolic link", "/run, not /var/run", "sard-agent.service", "a step is running", "D13"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help lacks %q:\n%s", want, stdout)
		}
	}
}

func TestSecretWithoutASubcommandNamesThem(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	code, _, stderr := runAgent("repo")
	assertCode(t, code, exitUsage)
	for _, want := range []string{"init", "list", "add", "show", "remove", "password"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q: %q", want, stderr)
		}
	}
}

func TestSecretIsDispatchedFromTheAgentCommandLine(t *testing.T) {
	t.Parallel()
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

func TestRepoAddHelpDescribesTheS3Flags(t *testing.T) {
	t.Parallel()
	h := newSetupHost(t)
	code, stdout, _ := h.sudo("repo", "add", "--help")
	assertCode(t, code, exitOK)
	for _, want := range []string{
		"--access-key-id", "--secret-key-stdin", "--secret-key-from-file", "--region", "--connect-timeout",
		"never the value of a flag", "secrets/restic-<name>.env", "AWS_ACCESS_KEY_ID", "s3:https://",
		"STORAGE_ACCESS_DENIED", "S3_KEY_REJECTED", "BUCKET_NOT_FOUND", "ADDRESS_INVALID", "credentials updated",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "only a local path") {
		t.Errorf("help still calls s3: unsupported:\n%s", stdout)
	}
}

// V3: the help of the key commands says what the commands promise.
func TestKeyHelpSaysWhatTheCommandsPromise(t *testing.T) {
	t.Parallel()
	h := newSetupHost(t)
	for _, c := range []struct {
		words []string
		wants []string
	}{
		{[]string{"repo", "key", "add"}, []string{"verifier", "recovery", "other", "shown once", "stored nowhere else", "KEY_OUTPUT_MISSING", "KEY_EXISTS", "sard:<purpose>"}},
		{[]string{"repo", "key", "list"}, []string{"CURRENT", "SINGLE_KEY", "NO_RECOVERY_KEY", "single_key"}},
		{[]string{"repo", "key", "remove"}, []string{"master key", "create a new repository", "CONFIRMATION_REQUIRED", "KEY_IN_USE", "LAST_KEY"}},
		{[]string{"repo", "add"}, []string{"recovery key", "RECOVERY_KEY_OUTPUT_MISSING", "RECOVERY_KEY_FAILED"}},
		{[]string{"repo", "init"}, []string{"recovery key", "RECOVERY_KEY_OUTPUT_MISSING", "RECOVERY_KEY_FAILED"}},
	} {
		code, stdout, _ := h.sudo(append(c.words, "--help")...)
		assertCode(t, code, exitOK)
		for _, want := range c.wants {
			if !strings.Contains(stdout, want) {
				t.Errorf("%v: help lacks %q:\n%s", c.words, want, stdout)
			}
		}
	}
}

func TestRepoKeyWithoutASubcommandNamesThemAndItsHelpListsThem(t *testing.T) {
	t.Parallel()
	code, _, stderr := runAgent("repo", "key")
	assertCode(t, code, exitUsage)
	for _, want := range []string{"add", "list", "remove"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q: %q", want, stderr)
		}
	}
	code, stdout, _ := runAgent("repo", "key", "--help")
	assertCode(t, code, exitOK)
	for _, want := range []string{"repo key add", "repo key list", "repo key remove"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help lacks %q:\n%s", want, stdout)
		}
	}
}
