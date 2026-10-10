// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"strings"
	"testing"
)

// Rule "Отказ хранилища называет свой класс и причину".

func TestTheRefusalOfAnS3StorageOnTheFirstAccessNamesItsClassCauseAndWhatToDo(t *testing.T) {
	for _, c := range []struct {
		sub, line string
		code      int
		reason    string
		text      string
	}{
		{"cat", "Fatal: unable to open config file: Stat: Access Denied.", exitUsage, "STORAGE_ACCESS_DENIED", "Access Denied"},
		{"cat", "Fatal: unable to open config file: Stat: The Access Key Id you provided does not exist in our records.", exitUsage, "S3_KEY_REJECTED", "does not exist in our records"},
		{"cat", "Fatal: unable to open config file: Stat: The request signature we calculated does not match the signature you provided.", exitUsage, "S3_KEY_REJECTED", "signature"},
		{"cat", "Fatal: unable to open config file: Stat: Get \"https://s3.example.com/bucket-b/extra/config\": dial tcp: lookup s3.example.com: no such host", exitTemporary, "BACKEND_UNAVAILABLE", "no such host"},
		{"cat", "Fatal: unable to open config file: Stat: unexpected response 418", exitAgentError, "BACKEND_REFUSED", "unexpected response 418"},
	} {
		h := newSetupHost(t)
		h.s3Repo().answers(c.sub, c.line, 1)
		code, stdout, stderr := h.s3Cmd()
		assertRefusal(t, code, stderr, c.code, c.reason)
		for _, want := range []string{c.text, s3Address} {
			if !strings.Contains(stderr, want) {
				t.Errorf("%s: stderr lacks %q:\n%s", c.reason, want, stderr)
			}
		}
		h.assertS3ValuesHidden(stdout, stderr)
		h.assertNoS3Traces()
	}
}

// П12: a refusal of init leaves the env file and the password file, and
// says so, whatever its class.
func TestARefusalOfAnS3StorageWhileCreatingKeepsTheEnvFileAndThePasswordForARepeat(t *testing.T) {
	for _, c := range []struct{ line, reason, text string }{
		{"Fatal: create repository at " + s3Address + " failed: client.PutObject: Forbidden: Operation is not allowed for this key.", "STORAGE_ACCESS_DENIED", "Operation is not allowed for this key"},
		{"Fatal: create repository at " + s3Address + " failed: The specified bucket does not exist", "BUCKET_NOT_FOUND", "bucket-b"},
		{"Fatal: create repository at s3:http://garage:3900/bucket-b/extra failed: client.MakeBucket: Forbidden: Access key KEY-ID-1 is not allowed to create buckets", "BUCKET_NOT_FOUND", "may not create it"},
	} {
		h := newSetupHost(t)
		h.s3Repo().answers("init", c.line, 1)
		code, stdout, stderr := h.s3Cmd()
		assertRefusal(t, code, stderr, exitUsage, c.reason)
		for _, want := range []string{c.text, "will be used when the command is repeated", h.envPath(), h.path("secrets/restic-extra.pass")} {
			if !strings.Contains(stderr, want) {
				t.Errorf("%s: stderr lacks %q:\n%s", c.reason, want, stderr)
			}
		}
		for _, p := range []string{h.envPath(), h.path("secrets/restic-extra.pass")} {
			h.assertOwner(p, serviceUID)
			h.assertMode(p, 0o600)
		}
		h.assertAbsent(h.path("agent.d/repo-extra.yaml"))
		if left := h.tempFilesIn(h.secretsDir()); len(left) != 0 || h.sd.touched() {
			t.Errorf("temporary files %v, systemctl %v", left, h.sd.calls)
		}
		h.assertS3ValuesHidden(stdout, stderr)
	}
}

func TestAnAccessDeniedOfS3NamesBothCausesAndTheBucket(t *testing.T) {
	h := newSetupHost(t)
	h.s3Repo().answers("cat", "Fatal: unable to open config file: Stat: Access Denied.", 1)
	_, _, stderr := h.s3Cmd()
	for _, want := range []string{"key id", "secret", "read, write and delete in the bucket bucket-b"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
}

func TestTheTextOfResticInTheMessageIsScrubbedOfTheKeyAndThePassword(t *testing.T) {
	h := newSetupHost(t)
	h.s3Repo().answers("cat", "Fatal: the storage said "+s3Marker+" and "+passMarker, 1)
	key := h.path("outside/key")
	h.write(key, s3Marker, 0o600)
	h.stdinIs(passMarker + "\n")
	code, stdout, stderr := h.sudo("repo", "add", "extra", s3Address, "--access-key-id", keyID1,
		"--secret-key-from-file", key, "--password-stdin", "--config", "C")
	assertCode(t, code, exitAgentError)
	if !strings.Contains(stderr, "[REDACTED]") || strings.Contains(stderr, "***") {
		t.Fatalf("stderr %q", stderr)
	}
	h.assertS3ValuesHidden(stdout, stderr)
}

func TestNoLineOfTheAuditAndNoOutputOfASuccessfulS3CommandShowsASecret(t *testing.T) {
	h := newSetupHost(t)
	code, stdout, stderr := h.s3Cmd("--region", "ru-central1")
	assertCode(t, code, exitOK)
	h.assertS3ValuesHidden(stdout, stderr)
	for _, c := range h.restic.calls {
		for _, a := range c.args {
			if strings.Contains(a, keyID1) || strings.Contains(a, s3Marker) {
				t.Errorf("restic %s got a key in an argument %q", c.sub, a)
			}
		}
	}
}

func TestAPasswordWithSpacesAroundItIsScrubbedToo(t *testing.T) {
	h := newSetupHost(t)
	h.s3Repo().answers("cat", "Fatal: the storage said "+passMarker, 1)
	h.stdinIs("  " + passMarker + " \t\n")
	key := h.path("outside/key")
	h.write(key, s3Marker, 0o600)
	code, stdout, stderr := h.sudo("repo", "add", "extra", s3Address, "--access-key-id", keyID1,
		"--secret-key-from-file", key, "--password-stdin", "--config", "C")
	assertCode(t, code, exitAgentError)
	h.assertS3ValuesHidden(stdout, stderr)
}

// П29: restic cat config takes no lock; a key that may not write fails on
// the command that does, before any file is written.
const lockDenied = "Fatal: unable to create lock in backend: client.PutObject: Forbidden: Operation is not allowed for this key."

func TestAReadOnlyKeyWithAPasswordFromAFileIsRefusedWithoutAQuestionAndWithoutFiles(t *testing.T) {
	h := newSetupHost(t)
	h.s3Repo().initialized, h.s3Repo().password = true, passMarker
	src := h.path("outside/F")
	h.write(src, passMarker+"\n", 0o600)
	h.s3Repo().answers("snapshots", lockDenied, 1)
	term := h.terminalIs()
	code, stdout, stderr := h.sudo("repo", "add", "extra", s3Address, "--access-key-id", keyID1, "--secret-key-from-file", src, "--password-from-file", src, "--config", "C")
	assertRefusal(t, code, stderr, exitUsage, "STORAGE_ACCESS_DENIED")
	if !strings.Contains(stderr, "Operation is not allowed for this key") || strings.Contains(stdout, "attached") || len(term.prompts) != 0 {
		t.Errorf("stdout %q stderr %q prompts %v", stdout, stderr, term.prompts)
	}
	h.assertNoS3Traces()
}

func TestAReadOnlyKeyWithATerminalPasswordIsRefusedAfterThePasswordAndWithoutFiles(t *testing.T) {
	h := newSetupHost(t)
	h.s3Repo().initialized, h.s3Repo().password = true, passMarker
	h.s3Repo().script = func(sub string, _ []string) (string, int, bool) { return lockDenied, 1, sub == "snapshots" }
	term := h.terminalIs(s3Marker, s3Marker, passMarker, passMarker)
	code, stdout, stderr := h.sudo("repo", "add", "extra", s3Address, "--access-key-id", keyID1, "--config", "C")
	assertRefusal(t, code, stderr, exitUsage, "STORAGE_ACCESS_DENIED")
	if strings.Contains(stdout, "attached") || len(term.prompts) == 0 {
		t.Errorf("stdout %q prompts %v", stdout, term.prompts)
	}
	h.assertNoS3Traces()
}
