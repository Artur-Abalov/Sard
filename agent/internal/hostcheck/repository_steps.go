// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck

import (
	"fmt"
	"slices"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/restickeys"
)

// RepositoryOpenChecks are the checks the files of the repository and one
// restic cat config answer (Ч3), in their order.
func RepositoryOpenChecks() []string {
	return []string{CheckRepositoryStorage, CheckRepositoryCredentials, CheckRepositoryInitialized, CheckRepositoryPassword}
}

// RepositoryChecks are the checks of one repository that every run makes, in
// their order (Ч3): the open checks, then the id, the keys (V3, K16) and the locks.
func RepositoryChecks() []string {
	return append(RepositoryOpenChecks(), CheckRepositoryID, CheckRepositoryKeys, CheckRepositoryLocks)
}

// KeysLine is the line of repository.keys (V3, K16): the warnings are those
// of restickeys.Warnings, the one rule of what a list of keys earns (K13); the
// repository has a key issued for recovery, or the operator is told to issue one.
func KeysLine(repo string, total int, warnings []string) Line {
	line := Line{Check: CheckRepositoryKeys, Subject: repo, Status: OK}
	switch {
	case !slices.Contains(warnings, restickeys.WarnNoRecoveryKey):
		line.Message = plural(total, "key") + ", recovery key present"
		return line
	case slices.Contains(warnings, restickeys.WarnSingleKey):
		line.Message = "no recovery key; only one key"
	default:
		line.Message = "no recovery key"
	}
	line.Status = Warn
	line.Hint = "sudo sard-agent repo key add " + repo + " --for recovery"
	return line
}

// RepositoryStepOf is the open check a refusal ends: the storage is the one
// that fails for everything that does not belong to a later check.
func RepositoryStepOf(reason refusal.Reason) string {
	switch reason {
	case refusal.S3KeyRejected, refusal.StorageAccessDenied, refusal.BucketNotFound, refusal.SSHKeyNotAuthorized,
		refusal.HostKeyMismatch, refusal.HostKeyRejected, refusal.HostKeyChanged, refusal.BackendRefused:
		return CheckRepositoryCredentials
	case refusal.RepositoryNotInitialized, refusal.ResticOutputUnexpected:
		return CheckRepositoryInitialized
	case refusal.WrongPassword, refusal.PasswordFileEmpty:
		return CheckRepositoryPassword
	}
	return CheckRepositoryStorage
}

// repositoryOKMessages are what an OK line of the open checks says.
var repositoryOKMessages = map[string]string{
	CheckRepositoryStorage:     "the storage at %s answers",
	CheckRepositoryCredentials: "the storage accepts the credentials",
	CheckRepositoryInitialized: "a repository is initialized at the address",
	CheckRepositoryPassword:    "the password file opens the repository",
}

// RepositoryOKMessage is the message of an OK line of an open check; the
// storage is named by address, which the caller has redacted.
func RepositoryOKMessage(check, address string) string {
	if check == CheckRepositoryStorage {
		return fmt.Sprintf(repositoryOKMessages[check], address)
	}
	return repositoryOKMessages[check]
}
