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
		LocalPathInvalid, RevealRequired, RepositoryConflict, ConfigLocked, ConfigWrite, ServiceRestartFailed}
	if len(classes) != len(reasons) {
		t.Errorf("%d reasons have a class, %d exist", len(classes), len(reasons))
	}
	for _, r := range reasons {
		if Fail(r, "x").Class == 0 {
			t.Errorf("%s has no class", r)
		}
	}
}
