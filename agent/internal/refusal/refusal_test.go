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
		RestoreIncomplete, TargetWrite, TargetBusy, SetprivMissing, ResticFailed, CleanupFailed, PluginFailed, StaleLocksForeign}
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

// R2c: a plugin's reason that does not fit the pattern of reason codes is worded
// as the agent's own PLUGIN_FAILED.
func TestPluginFailedIsAnAgentError(t *testing.T) {
	if PluginFailed != "PLUGIN_FAILED" || ClassOf(PluginFailed) != ClassAgentError {
		t.Errorf("PluginFailed = %q, class %d", PluginFailed, ClassOf(PluginFailed))
	}
}

// Scenario: В причинах агента нет причин PostgreSQL (R2c-3): the reasons of a
// database are those of its plugin.
func TestTheReasonsOfTheAgentHoldNoReasonOfADatabase(t *testing.T) {
	for _, code := range []Reason{"PG_CLIENT_MISSING", "PG_CLIENT_TOO_OLD", "PG_SERVER_TOO_OLD", "PG_CONNECT", "CREATEDB_REQUIRED",
		"SUPERUSER_REQUIRED", "GLOBALS_SNAPSHOT_MISSING", "GLOBALS_TABLESPACES", "ROLES_EXIST", "DATABASE_EXISTS",
		"PG_GLOBALS_FAILED", "PG_CREATE_DATABASE", "PG_RESTORE_FAILED"} {
		if _, found := classes[code]; found {
			t.Errorf("the agent has the reason %s", code)
		}
	}
	if len(classes) < 60 {
		t.Errorf("only %d reasons were looked at", len(classes))
	}
}
