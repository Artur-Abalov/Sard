// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"strings"
	"testing"
)

// Rule "Отказ хранилища называет свой класс и причину".

func TestTheRefusalOfAnS3StorageNamesItsClassCauseAndAddress(t *testing.T) {
	for _, c := range []struct {
		sub, line string
		code      int
		reason    string
		text      string
	}{
		{"cat", "Fatal: unable to open config file: Stat: Access Denied.", exitUsage, "STORAGE_ACCESS_DENIED", "Access Denied"},
		{"cat", "Fatal: unable to open config file: Stat: The Access Key Id you provided does not exist in our records.", exitUsage, "S3_KEY_REJECTED", "does not exist in our records"},
		{"cat", "Fatal: unable to open config file: Stat: The request signature we calculated does not match the signature you provided.", exitUsage, "S3_KEY_REJECTED", "signature"},
		{"cat", "unable to create lock in backend: client.PutObject: Forbidden: Operation is not allowed for this key.", exitUsage, "STORAGE_ACCESS_DENIED", "Operation is not allowed for this key"},
		{"init", "Fatal: create repository at " + s3Address + " failed: client.PutObject: Forbidden: Operation is not allowed for this key.", exitUsage, "STORAGE_ACCESS_DENIED", "Operation is not allowed for this key"},
		{"init", "Fatal: create repository at " + s3Address + " failed: The specified bucket does not exist", exitUsage, "BUCKET_NOT_FOUND", "bucket-b"},
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
		if c.sub == "cat" {
			h.assertNoS3Traces()
		}
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
	if !strings.Contains(stderr, "[REDACTED]") {
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
