// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck_test

import (
	"slices"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
)

// Ч3: the first four checks of a repository, in their order.
func TestTheOpenChecksOfARepositoryAreTheFirstFourInOrder(t *testing.T) {
	want := []string{"repository.storage", "repository.credentials", "repository.initialized", "repository.password"}
	if got := hostcheck.RepositoryOpenChecks(); !slices.Equal(got, want) {
		t.Errorf("RepositoryOpenChecks = %v", got)
	}
}

// Ч3: the check a refusal ends.
func TestARefusalEndsTheOpenCheckItBelongsTo(t *testing.T) {
	cases := map[string][]refusal.Reason{
		hostcheck.CheckRepositoryStorage: {refusal.BackendUnavailable, refusal.Timeout, refusal.Reason("UNKNOWN_REASON")},
		hostcheck.CheckRepositoryCredentials: {
			refusal.S3KeyRejected, refusal.StorageAccessDenied, refusal.BucketNotFound, refusal.SSHKeyNotAuthorized,
			refusal.HostKeyMismatch, refusal.HostKeyRejected, refusal.HostKeyChanged, refusal.BackendRefused,
		},
		hostcheck.CheckRepositoryInitialized: {refusal.RepositoryNotInitialized, refusal.ResticOutputUnexpected},
		hostcheck.CheckRepositoryPassword:    {refusal.WrongPassword, refusal.PasswordFileEmpty},
	}
	for check, reasons := range cases {
		for _, reason := range reasons {
			if got := hostcheck.RepositoryStepOf(reason); got != check {
				t.Errorf("RepositoryStepOf(%s) = %q, want %q", reason, got, check)
			}
		}
	}
}

func TestTheOKLineOfAnOpenCheckSaysWhatWasFound(t *testing.T) {
	cases := map[string]string{
		hostcheck.CheckRepositoryStorage:     "the storage at s3:https://h/b answers",
		hostcheck.CheckRepositoryCredentials: "the storage accepts the credentials",
		hostcheck.CheckRepositoryInitialized: "a repository is initialized at the address",
		hostcheck.CheckRepositoryPassword:    "the password file opens the repository",
		hostcheck.CheckRepositoryID:          "",
	}
	for check, want := range cases {
		if got := hostcheck.RepositoryOKMessage(check, "s3:https://h/b"); got != want {
			t.Errorf("RepositoryOKMessage(%s) = %q, want %q", check, got, want)
		}
	}
}
