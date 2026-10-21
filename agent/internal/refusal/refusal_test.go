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
		HostKeyUnconfirmed, HostKeyMismatch, HostKeyRejected, HostKeyChanged, RepositoryNotInitialized,
		SnapshotUnknown, SnapshotAmbiguous, TargetInvalid, TargetNotEmpty, RootTargetRefused, PathNotInSnapshot,
		RestoreIncomplete, TargetWrite, TargetBusy, SetprivMissing, ResticFailed, CleanupFailed, StaleLocksForeign,
		PGClientMissing, PGClientTooOld, PGServerTooOld, PGConnect, CreatedbRequired, SuperuserRequired,
		GlobalsSnapshotMissing, GlobalsTablespaces, RolesExist, DatabaseExists, PGGlobalsFailed, PGCreateDatabase, PGRestoreFailed,
		KeyPurposeMissing, KeyPurposeInvalid, KeyLabelRequired, LabelInvalid, KeyExists, KeyOutputMissing,
		KeyFileExists, KeyFileRejected, KeyFileWrite, KeyIDAmbiguous, KeyInUse, LastKey, ConfirmationRequired,
		ConfirmationRejected, RecoveryKeyOutputMissing, RecoveryKeyFailed, VerifierReadOnly}
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

// A8c-2: the classes of the reasons of repo restore (the table of classes
// of docs/specs/agent/repo-restore.feature).
func TestTheReasonsOfRepoRestoreHaveTheClassesOfTheSpecification(t *testing.T) {
	for reason, class := range map[Reason]Class{
		SnapshotUnknown: ClassUsage, SnapshotAmbiguous: ClassUsage, TargetInvalid: ClassUsage,
		TargetNotEmpty: ClassUsage, RootTargetRefused: ClassUsage, PathNotInSnapshot: ClassUsage,
		RestoreIncomplete: ClassAgentError, TargetWrite: ClassWrite, TargetBusy: ClassTemporary, SetprivMissing: ClassAgentError,
	} {
		if got := ClassOf(reason); got != class {
			t.Errorf("%s: class %d, want %d", reason, got, class)
		}
	}
}

// R2a: the reasons only the restore from the console has.
func TestTheReasonsOfTheRestoreFromTheConsoleHaveTheirClasses(t *testing.T) {
	for reason, class := range map[Reason]Class{ResticFailed: ClassAgentError, CleanupFailed: ClassWrite} {
		if got := ClassOf(reason); got != class {
			t.Errorf("%s: class %d, want %d", reason, got, class)
		}
	}
}

// R2b: the reasons only the restore of a PostgreSQL database from the console has.
func TestTheReasonsOfTheRestoreOfADatabaseHaveTheirClasses(t *testing.T) {
	for reason, class := range map[Reason]Class{
		PGClientMissing: ClassAgentError, PGClientTooOld: ClassAgentError, PGServerTooOld: ClassUsage,
		PGConnect: ClassTemporary, CreatedbRequired: ClassUsage, SuperuserRequired: ClassUsage,
		GlobalsSnapshotMissing: ClassUsage, GlobalsTablespaces: ClassUsage, RolesExist: ClassUsage,
		DatabaseExists: ClassUsage, PGGlobalsFailed: ClassAgentError, PGCreateDatabase: ClassAgentError, PGRestoreFailed: ClassAgentError,
	} {
		if got := ClassOf(reason); got != class {
			t.Errorf("%s: class %d, want %d", reason, got, class)
		}
	}
	if string(PGClientMissing) != "PG_CLIENT_MISSING" || string(PGRestoreFailed) != "PG_RESTORE_FAILED" {
		t.Errorf("names %q %q", PGClientMissing, PGRestoreFailed)
	}
}
