// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package refusal

import "testing"

// A failure without a class would exit 0: every reason has one.
func TestEveryReasonHasAClass(t *testing.T) {
	reasons := []Reason{RepositoryUnknown, CryptoProviderUnsupported, PasswordFileMissing, PasswordFileEmpty,
		EnvFileMissing, EnvFileInvalid, SecretFileRejected, ResticNotFound, ResticTooOld, ResticUnusable,
		ResticOutputUnexpected, RepositoryExists, WrongPassword, BackendUnavailable, BackendRefused,
		Interrupted, Timeout, InitInProgress, PasswordFileWrite, LockWrite,
		PrivilegesRequired, ServiceUserUnknown, NameInvalid, DefinedInConfig, PathInUse, SecretSourceMissing,
		SecretSourceConflict, SecretEmpty, SecretTooLarge, SecretMismatch, BackendNotSupported,
		LocalPathInvalid, RevealRequired, RepositoryConflict, ConfigLocked, ConfigWrite, ServiceRestartFailed,
		AddressInvalid, SecretInvalid, S3KeyRejected, StorageAccessDenied, BucketNotFound,
		SSHClientMissing, SSHClientFailed, SSHHomeInvalid, SSHFileRejected, SSHKeyNotAuthorized,
		HostKeyUnconfirmed, HostKeyMismatch, HostKeyRejected, HostKeyChanged}
	if len(classes) != len(reasons) {
		t.Errorf("%d reasons have a class, %d exist", len(classes), len(reasons))
	}
	for _, r := range reasons {
		if Fail(r, "x").Class == 0 {
			t.Errorf("%s has no class", r)
		}
	}
}

func TestFailureTextIsReasonAndDetailExceptForTheTwoThatStandAlone(t *testing.T) {
	cases := []struct {
		f    *Failure
		want string
	}{
		{Fail(WrongPassword, "wrong %s", "key"), "WRONG_PASSWORD: wrong key"},
		{Fail(SecretFileRejected, "mode 0644"), "mode 0644"},
		{&Failure{Detail: "bad config"}, "bad config"},
	}
	for _, c := range cases {
		if got := c.f.Error(); got != c.want {
			t.Errorf("Error() = %q, want %q", got, c.want)
		}
	}
}

// A8b-2 (Р36-Р40): the classes of the reasons of the ssh setup, which
// become exit codes 1, 2 and 5.
func TestTheReasonsOfTheSSHSetupHaveTheClassesOfTheSpecification(t *testing.T) {
	for reason, class := range map[Reason]Class{
		SSHClientMissing: ClassAgentError, SSHClientFailed: ClassAgentError,
		SSHHomeInvalid: ClassUsage, SSHFileRejected: ClassUsage, SSHKeyNotAuthorized: ClassUsage, HostKeyUnconfirmed: ClassUsage,
		HostKeyMismatch: ClassTrust, HostKeyRejected: ClassTrust, HostKeyChanged: ClassTrust,
	} {
		if got := ClassOf(reason); got != class {
			t.Errorf("%s: class %d, want %d", reason, got, class)
		}
	}
}
