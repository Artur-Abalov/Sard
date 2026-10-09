// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoinit_test

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// remote is the target of repo add for an s3: address (A8b, Р34).
var remote = repoinit.Target{
	Name: "extra", Backend: "s3", PasswordFile: "/etc/sard/secrets/restic-extra.pass",
	Remote: true, Where: "s3:https://s3.example.com/bucket-b/extra", Bucket: "bucket-b",
	Scrub: func(s string) string { return strings.ReplaceAll(s, "S3-MARKER", "***") },
}

// Р34: the class of a refusal of the storage, from what restic printed.
func TestTheRefusalOfAnS3StorageHasItsClassAndCause(t *testing.T) {
	cases := []struct {
		cause  string
		kind   error
		reason refusal.Reason
		class  refusal.Class
		text   string
	}{
		{"Fatal: unable to open config file: Stat: Access Denied.", nil, refusal.StorageAccessDenied, refusal.ClassUsage, "Access Denied"},
		{"Fatal: unable to open config file: Stat: The Access Key Id you provided does not exist in our records.", nil, refusal.S3KeyRejected, refusal.ClassUsage, "does not exist in our records"},
		{"Fatal: unable to open config file: Stat: The request signature we calculated does not match the signature you provided.", nil, refusal.S3KeyRejected, refusal.ClassUsage, "signature"},
		{"Fatal: Stat: InvalidAccessKeyId: x", nil, refusal.S3KeyRejected, refusal.ClassUsage, "InvalidAccessKeyId"},
		{"Fatal: Stat: SignatureDoesNotMatch: x", nil, refusal.S3KeyRejected, refusal.ClassUsage, "SignatureDoesNotMatch"},
		{"unable to create lock in backend: client.PutObject: Forbidden: Operation is not allowed for this key.", nil, refusal.StorageAccessDenied, refusal.ClassUsage, "Operation is not allowed for this key"},
		{"Fatal: create repository at s3:https://s3.example.com/bucket-b/extra failed: The specified bucket does not exist", nil, refusal.BucketNotFound, refusal.ClassUsage, "bucket-b"},
		{"Fatal: create repository at s3:http://garage:3900/absent/main failed: Bucket not found: absent", nil, refusal.BucketNotFound, refusal.ClassUsage, "bucket-b"},
		{"Fatal: Stat: NoSuchBucket: x", nil, refusal.BucketNotFound, refusal.ClassUsage, "bucket-b"},
		{"Fatal: Stat: Get \"https://s3.example.com/bucket-b/extra/config\": dial tcp: lookup s3.example.com: no such host", restic.ErrNetwork, refusal.BackendUnavailable, refusal.ClassTemporary, "no such host"},
		{"Fatal: unable to open config file: Stat: unexpected response 418", nil, refusal.BackendRefused, refusal.ClassAgentError, "unexpected response 418"},
	}
	for _, c := range cases {
		f := repoinit.FromRestic(t.Context(), exitErr(c.cause, c.kind), remote)
		if f.Reason != c.reason || f.Class != c.class {
			t.Errorf("%q: %+v", c.cause, f)
			continue
		}
		if !strings.Contains(f.Detail, c.text) || !strings.Contains(f.Detail, remote.Where) {
			t.Errorf("%q: the message lacks the cause or the address: %q", c.cause, f.Detail)
		}
	}
}

func TestAccessDeniedNamesBothPossibleCausesAndTheBucket(t *testing.T) {
	f := repoinit.FromRestic(t.Context(), exitErr("Fatal: Stat: Access Denied.", nil), remote)
	for _, want := range []string{"key id", "secret", "read, write and delete in the bucket bucket-b"} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("the message lacks %q: %s", want, f.Detail)
		}
	}
}

func TestAMissingBucketTellsWhatToDo(t *testing.T) {
	f := repoinit.FromRestic(t.Context(), exitErr("Fatal: The specified bucket does not exist", nil), remote)
	if !strings.Contains(f.Detail, "create the bucket") {
		t.Errorf("message: %s", f.Detail)
	}
}

func TestTheCauseInTheMessageOfARemoteStorageIsScrubbed(t *testing.T) {
	f := repoinit.FromRestic(t.Context(), exitErr("Fatal: Access Denied for S3-MARKER", nil), remote)
	if strings.Contains(f.Detail, "S3-MARKER") || !strings.Contains(f.Detail, "***") {
		t.Errorf("message: %s", f.Detail)
	}
}

func TestAWrongPasswordOfARemoteStorageIsStillAWrongPassword(t *testing.T) {
	f := repoinit.FromRestic(t.Context(), exitErr("wrong", restic.ErrWrongPassword), remote)
	if f.Reason != refusal.WrongPassword {
		t.Errorf("%+v", f)
	}
}

// A5b: a target that is not remote keeps the old reasons whatever restic said.
func TestAccessDeniedOfALocalTargetIsStillBackendRefused(t *testing.T) {
	local := remote
	local.Remote = false
	f := repoinit.FromRestic(t.Context(), exitErr("Fatal: Access Denied", nil), local)
	if f.Reason != refusal.BackendRefused {
		t.Errorf("%+v", f)
	}
}

// Р33: the last reason of a retry that restic printed.
func TestTheLastRetryReasonIsTakenFromResticsStderr(t *testing.T) {
	stderr := "Load(<config/0000000000>, 0, 0) returned error, retrying after 552ms: dial tcp 192.0.2.1:443: connect: connection refused\n" +
		"Load(<config/0000000000>, 0, 0) returned error, retrying after 1.2s: dial tcp 192.0.2.1:443: i/o timeout\n" +
		"something else\n"
	if got := repoinit.RetryReason(stderr); got != "dial tcp 192.0.2.1:443: i/o timeout" {
		t.Errorf("reason = %q", got)
	}
	if got := repoinit.RetryReason("nothing to retry\n"); got != "" {
		t.Errorf("reason = %q", got)
	}
}

// F3: only what restic said decides the class, and not the address in it.
func TestTheAddressInTheCauseDoesNotDecideTheClass(t *testing.T) {
	for _, addr := range []string{
		"s3:https://forbidden.example.com/accessdenied/extra",
		"https://s3.example.com/nosuchbucket/config",
		"s3:s3.example.com/Access-Denied",
	} {
		f := repoinit.FromRestic(t.Context(), exitErr("Fatal: unable to open config file: Stat: Get \""+addr+"\": unexpected response 418", nil), remote)
		if f.Reason != refusal.BackendRefused {
			t.Errorf("%s: %+v", addr, f)
		}
	}
}

func TestAnErrorOfTheAgentIsNotARefusalOfTheStorage(t *testing.T) {
	for _, err := range []error{
		fmt.Errorf("env_file: open /etc/sard/secrets/x.env: permission denied"),
		fmt.Errorf("restic cat: %w", &os.PathError{Op: "fork/exec", Path: "/usr/libexec/sard/restic", Err: syscall.EACCES}),
		exitErr("", nil),
	} {
		f := repoinit.FromRestic(t.Context(), err, remote)
		if f.Reason != refusal.BackendRefused {
			t.Errorf("%v: %+v", err, f)
		}
	}
}

// An sftp: target names the directory, not a bucket.
var sftpTarget = repoinit.Target{
	Name: "extra", Backend: "sftp", PasswordFile: "/etc/sard/secrets/restic-extra.pass",
	Remote: true, Where: "sftp:backup@nas.example.com:/srv/extra", Directory: "/srv/extra",
	Scrub: func(s string) string { return s },
}

func TestAnSFTPDirectoryWithoutTheRightsNamesTheDirectoryAndTheCause(t *testing.T) {
	for _, cause := range []string{
		"unable to create lock in backend: OpenFile /srv/extra/locks/1a2b: permission denied",
		"Fatal: create repository at sftp:backup@nas.example.com:/srv/extra failed: sftp: MkdirAll /srv/extra/keys: permission denied",
	} {
		f := repoinit.FromRestic(t.Context(), exitErr(cause, nil), sftpTarget)
		if f.Reason != refusal.StorageAccessDenied || f.Class != refusal.ClassUsage {
			t.Fatalf("%q: %+v", cause, f)
		}
		for _, want := range []string{"permission denied", "/srv/extra", "nas.example.com", "read, write and delete"} {
			if !strings.Contains(f.Detail, want) {
				t.Errorf("detail lacks %q: %s", want, f.Detail)
			}
		}
		if strings.Contains(f.Detail, "bucket") || strings.Contains(f.Detail, "secret") {
			t.Errorf("detail talks of s3: %s", f.Detail)
		}
	}
}
