// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"strings"
	"syscall"
	"testing"
)

func (h *setupHost) envPath() string { return h.path("secrets/restic-extra.env") }

// s3Repo is the repository of A_S3 in the fake restic.
func (h *setupHost) s3Repo() *fakeRepo { return h.restic.repo(s3Address) }

// Rule "Секретный ключ S3 передаётся как секрет и хранится только в env-файле пользователя службы".

func TestTheS3KeysFromStandardInputAreWrittenToTheEnvFileOfTheServiceUser(t *testing.T) {
	h := newSetupHost(t)
	mainBefore := h.fileContent(h.cfgPath)
	code, stdout, stderr := h.s3Cmd("--region", "ru-central1")
	assertCode(t, code, exitOK)
	h.assertOwner(h.envPath(), serviceUID)
	h.assertMode(h.envPath(), 0o600)
	want := "AWS_ACCESS_KEY_ID=KEY-ID-1\nAWS_SECRET_ACCESS_KEY=S3-MARKER\nAWS_DEFAULT_REGION=ru-central1\n"
	if got := h.fileContent(h.envPath()); got != want {
		t.Fatalf("env file %q", got)
	}
	fragment := h.fileContent(h.path("agent.d/repo-extra.yaml"))
	for _, w := range []string{"name: extra", "url: " + s3Address, "password_file: " + h.path("secrets/restic-extra.pass"), "env_file: " + h.envPath()} {
		if !strings.Contains(fragment, w) {
			t.Errorf("fragment lacks %q:\n%s", w, fragment)
		}
	}
	if h.fileContent(h.cfgPath) != mainBefore {
		t.Fatal("the main config changed")
	}
	h.assertS3ValuesHidden(stdout, stderr, fragment)
}

func TestWithoutARegionTheEnvFileHasNoDefaultRegion(t *testing.T) {
	h := newSetupHost(t)
	code, _, _ := h.s3Cmd()
	assertCode(t, code, exitOK)
	if got, want := h.fileContent(h.envPath()), "AWS_ACCESS_KEY_ID=KEY-ID-1\nAWS_SECRET_ACCESS_KEY=S3-MARKER\n"; got != want {
		t.Fatalf("env file %q", got)
	}
}

func TestTheModeOfTheEnvFileDoesNotDependOnTheUmask(t *testing.T) {
	old := syscall.Umask(0)
	defer syscall.Umask(old)
	h := newSetupHost(t)
	code, _, _ := h.s3Cmd()
	assertCode(t, code, exitOK)
	h.assertMode(h.envPath(), 0o600)
}

func TestTheS3SecretKeyFromAFileLeavesTheFileAlone(t *testing.T) {
	h := newSetupHost(t)
	src := h.path("outside/F")
	h.write(src, s3Marker, 0o644)
	code, _, _ := h.sudo("repo", "add", "extra", s3Address, "--access-key-id", keyID1, "--secret-key-from-file", src, "--config", "C")
	assertCode(t, code, exitOK)
	if !strings.Contains(h.fileContent(h.envPath()), "AWS_SECRET_ACCESS_KEY=S3-MARKER\n") {
		t.Fatalf("env file %q", h.fileContent(h.envPath()))
	}
	if _, ok := h.fsys.ownerOf(src); ok || h.fileContent(src) != s3Marker {
		t.Fatal("the source file was touched")
	}
	h.assertMode(src, 0o644)
}

func TestTheS3SecretKeyFromTheTerminalIsAskedTwice(t *testing.T) {
	h := newSetupHost(t)
	term := h.terminalIs(s3Marker, s3Marker)
	code, _, stderr := h.sudo("repo", "add", "extra", s3Address, "--access-key-id", keyID1, "--config", "C")
	assertCode(t, code, exitOK)
	if len(term.prompts) != 2 {
		t.Fatalf("prompts %q", term.prompts)
	}
	if !strings.Contains(h.fileContent(h.envPath()), "AWS_SECRET_ACCESS_KEY=S3-MARKER\n") {
		t.Fatalf("env file %q", h.fileContent(h.envPath()))
	}
	h.assertS3ValuesHidden(stderr)
}

func TestTwoDifferentTerminalAnswersForTheS3SecretKeyAreRefusedBeforeTheStorage(t *testing.T) {
	h := newSetupHost(t)
	h.terminalIs(s3Marker, s3Marker+"-2")
	before := h.hostTree()
	code, stdout, stderr := h.sudo("repo", "add", "extra", s3Address, "--access-key-id", keyID1, "--config", "C")
	assertRefusal(t, code, stderr, exitUsage, "SECRET_MISMATCH")
	h.assertNoBackendCalls()
	h.assertHostUnchanged(before)
	h.assertS3ValuesHidden(stdout, stderr)
}

func TestWithoutATerminalAndWithoutAFlagTheS3SecretKeyIsNotAskedAndTheFlagsAreNamed(t *testing.T) {
	h := newSetupHost(t)
	h.hung = &hungInput{}
	h.deps.stdin = h.hung
	before := h.hostTree()
	code, _, stderr := h.sudo("repo", "add", "extra", s3Address, "--access-key-id", keyID1, "--config", "C")
	assertRefusal(t, code, stderr, exitUsage, "SECRET_SOURCE_MISSING")
	for _, flag := range []string{"--secret-key-stdin", "--secret-key-from-file"} {
		if !strings.Contains(stderr, flag) {
			t.Errorf("stderr does not name %s: %q", flag, stderr)
		}
	}
	if h.hung.reads != 0 {
		t.Fatal("the command read standard input")
	}
	h.assertHostUnchanged(before)
}

func TestTwoSourcesOfSecretsAtOnceAreRefusedBeforeInputIsRead(t *testing.T) {
	for _, flags := range [][]string{
		{"--secret-key-stdin", "--password-stdin"},
		{"--secret-key-stdin", "--secret-key-from-file", "F"},
	} {
		h := newSetupHost(t)
		h.hung = &hungInput{}
		h.deps.stdin = h.hung
		before := h.hostTree()
		code, _, stderr := h.sudo(append([]string{"repo", "add", "extra", s3Address, "--access-key-id", keyID1, "--config", "C"}, flags...)...)
		assertRefusal(t, code, stderr, exitUsage, "SECRET_SOURCE_CONFLICT")
		if h.hung.reads != 0 {
			t.Fatal("the command read standard input")
		}
		h.assertHostUnchanged(before)
	}
}

func TestAKeyFromStandardInputAndAPasswordFromAFileAreCompatible(t *testing.T) {
	h := newSetupHost(t)
	h.s3Repo().initialized, h.s3Repo().password = true, passMarker
	src := h.path("outside/F")
	h.write(src, passMarker+"\n", 0o600)
	code, _, stderr := h.s3Cmd("--password-from-file", src)
	assertCode(t, code, exitOK)
	h.assertS3ValuesHidden(stderr)
}

func TestOneTrailingLineBreakOfTheS3SecretKeyIsDropped(t *testing.T) {
	for _, input := range []string{s3Marker + "\n", s3Marker + "\r\n", s3Marker} {
		h := newSetupHost(t)
		code, _, _ := h.s3At(s3Address, input)
		assertCode(t, code, exitOK)
		got := h.fileContent(h.envPath())
		if !strings.Contains(got, "AWS_SECRET_ACCESS_KEY=S3-MARKER\n") || strings.Contains(got, "\r") {
			t.Errorf("%q: env file %q", input, got)
		}
	}
}

func TestAnS3SecretKeyWithAControlCharacterIsRefusedWithoutTraces(t *testing.T) {
	for _, input := range []string{s3Marker + "\nX", s3Marker + "\n\n", s3Marker + "\rX", s3Marker + "\x00X"} {
		h := newSetupHost(t)
		before := h.hostTree()
		code, stdout, stderr := h.s3At(s3Address, input)
		assertRefusal(t, code, stderr, exitUsage, "SECRET_INVALID")
		h.assertNoBackendCalls()
		h.assertHostUnchanged(before)
		h.assertS3ValuesHidden(stdout, stderr)
	}
}

func TestAnEmptyOrTooLargeS3SecretKeyIsRefused(t *testing.T) {
	for input, reason := range map[string]string{"": "SECRET_EMPTY", "\n": "SECRET_EMPTY", strings.Repeat("A", 65537): "SECRET_TOO_LARGE"} {
		h := newSetupHost(t)
		before := h.hostTree()
		code, _, stderr := h.s3At(s3Address, input)
		assertRefusal(t, code, stderr, exitUsage, reason)
		h.assertHostUnchanged(before)
	}
}

func TestTheS3KeysReachResticOnlyThroughTheEnvironment(t *testing.T) {
	h := newSetupHost(t)
	code, _, _ := h.s3Cmd()
	assertCode(t, code, exitOK)
	var repoCalls int
	for _, c := range h.restic.calls {
		if c.sub != "version" {
			repoCalls++
			assertKeysInTheEnvironment(t, c)
		}
		for _, a := range c.args {
			if strings.Contains(a, s3Marker) {
				t.Errorf("restic %s got the key in an argument: %q", c.sub, a)
			}
		}
	}
	if repoCalls < 2 {
		t.Fatalf("restic calls: %v", h.restic.subs())
	}
}

func assertKeysInTheEnvironment(t *testing.T, c fakeCall) {
	t.Helper()
	if envValue(c.env, "AWS_ACCESS_KEY_ID") != keyID1 || envValue(c.env, "AWS_SECRET_ACCESS_KEY") != s3Marker {
		t.Errorf("restic %s ran without the keys in its environment", c.sub)
	}
	if envValue(c.env, "RESTIC_PASSWORD") != "" {
		t.Errorf("restic %s got RESTIC_PASSWORD", c.sub)
	}
	if c.runAs == nil || c.runAs.UID != serviceUID {
		t.Errorf("restic %s ran as %+v", c.sub, c.runAs)
	}
}
