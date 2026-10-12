// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package refusal is what every command of the agent that refuses tells the
// operator: a reason (a stable name), a class (which exit code) and a
// detail, plus the lock file that keeps two commands apart. It imports
// nothing of the agent, so everything may depend on it.
package refusal

import "fmt"

// Reason is the string every refusal carries (С8): a stable name the
// operator can search for and a test can check.
type Reason string

// The reasons of repo init and repo list.
const (
	RepositoryUnknown         Reason = "REPOSITORY_UNKNOWN"
	CryptoProviderUnsupported Reason = "CRYPTO_PROVIDER_UNSUPPORTED"
	PasswordFileMissing       Reason = "PASSWORD_FILE_MISSING"
	PasswordFileEmpty         Reason = "PASSWORD_FILE_EMPTY"
	EnvFileMissing            Reason = "ENV_FILE_MISSING"
	EnvFileInvalid            Reason = "ENV_FILE_INVALID"
	// SecretFileRejected is the STATUS of a list row whose secret file
	// A1 rejects; the message of repo init is A1's own text (Л5).
	SecretFileRejected Reason = "SECRET_FILE_REJECTED"
	ResticNotFound     Reason = "RESTIC_NOT_FOUND"
	ResticTooOld       Reason = "RESTIC_TOO_OLD"
	ResticUnusable     Reason = "RESTIC_UNUSABLE"
	// ResticOutputUnexpected: restic succeeded but printed no repository id.
	ResticOutputUnexpected Reason = "RESTIC_OUTPUT_UNEXPECTED"
	RepositoryExists       Reason = "REPOSITORY_EXISTS"
	WrongPassword          Reason = "WRONG_PASSWORD"
	BackendUnavailable     Reason = "BACKEND_UNAVAILABLE"
	BackendRefused         Reason = "BACKEND_REFUSED"
	Interrupted            Reason = "INTERRUPTED"
	Timeout                Reason = "TIMEOUT"
	InitInProgress         Reason = "INIT_IN_PROGRESS"
	PasswordFileWrite      Reason = "PASSWORD_FILE_WRITE"
	LockWrite              Reason = "LOCK_WRITE"
)

// The reasons of the host-setup commands (A8a, docs/specs/agent/host-setup.feature).
const (
	PrivilegesRequired   Reason = "PRIVILEGES_REQUIRED"
	ServiceUserUnknown   Reason = "SERVICE_USER_UNKNOWN"
	NameInvalid          Reason = "NAME_INVALID"
	DefinedInConfig      Reason = "DEFINED_IN_CONFIG"
	PathInUse            Reason = "PATH_IN_USE"
	SecretSourceMissing  Reason = "SECRET_SOURCE_MISSING"
	SecretSourceConflict Reason = "SECRET_SOURCE_CONFLICT"
	SecretEmpty          Reason = "SECRET_EMPTY"
	SecretTooLarge       Reason = "SECRET_TOO_LARGE"
	SecretMismatch       Reason = "SECRET_MISMATCH"
	BackendNotSupported  Reason = "BACKEND_NOT_SUPPORTED"
	LocalPathInvalid     Reason = "LOCAL_PATH_INVALID"
	RevealRequired       Reason = "REVEAL_REQUIRED"
	RepositoryConflict   Reason = "REPOSITORY_CONFLICT"
	ConfigLocked         Reason = "CONFIG_LOCKED"
	ConfigWrite          Reason = "CONFIG_WRITE"
	ServiceRestartFailed Reason = "SERVICE_RESTART_FAILED"
)

// The reasons of repo add for remote storage (A8b, Р28, Р29, Р34).
const (
	AddressInvalid      Reason = "ADDRESS_INVALID"
	SecretInvalid       Reason = "SECRET_INVALID"
	S3KeyRejected       Reason = "S3_KEY_REJECTED"
	StorageAccessDenied Reason = "STORAGE_ACCESS_DENIED"
	BucketNotFound      Reason = "BUCKET_NOT_FOUND"
)

// The reasons of repo add for an sftp: storage (A8b-2, Р36-Р40).
const (
	SSHClientMissing    Reason = "SSH_CLIENT_MISSING"
	SSHClientFailed     Reason = "SSH_CLIENT_FAILED"
	SSHHomeInvalid      Reason = "SSH_HOME_INVALID"
	SSHFileRejected     Reason = "SSH_FILE_REJECTED"
	SSHKeyNotAuthorized Reason = "SSH_KEY_NOT_AUTHORIZED"
	HostKeyUnconfirmed  Reason = "HOST_KEY_UNCONFIRMED"
	HostKeyMismatch     Reason = "HOST_KEY_MISMATCH"
	HostKeyRejected     Reason = "HOST_KEY_REJECTED"
	HostKeyChanged      Reason = "HOST_KEY_CHANGED"
)

// Class is the kind of a failure; the command maps it to an exit code
// (the numbers are A2b's, docs/adr/0025-grpc-error-model.md).
type Class int

// The classes repo init and repo list use.
const (
	ClassAgentError Class = iota + 1
	ClassUsage
	ClassExists
	ClassTemporary
	ClassWrite
	// ClassTrust: a key of a host the operator was asked to trust is not
	// the one expected, was not accepted, or changed (exit code 5).
	ClassTrust
)

var classes = map[Reason]Class{
	RepositoryUnknown: ClassUsage, CryptoProviderUnsupported: ClassUsage,
	PasswordFileMissing: ClassUsage, PasswordFileEmpty: ClassUsage,
	EnvFileMissing: ClassUsage, EnvFileInvalid: ClassUsage, SecretFileRejected: ClassUsage,
	WrongPassword:  ClassUsage,
	ResticNotFound: ClassAgentError, ResticTooOld: ClassAgentError, ResticUnusable: ClassAgentError,
	ResticOutputUnexpected: ClassAgentError, BackendRefused: ClassAgentError,
	RepositoryExists:   ClassExists,
	BackendUnavailable: ClassTemporary, Interrupted: ClassTemporary, Timeout: ClassTemporary, InitInProgress: ClassTemporary,
	PasswordFileWrite: ClassWrite, LockWrite: ClassWrite,

	PrivilegesRequired: ClassUsage, ServiceUserUnknown: ClassUsage, NameInvalid: ClassUsage,
	DefinedInConfig: ClassUsage, PathInUse: ClassUsage,
	SecretSourceMissing: ClassUsage, SecretSourceConflict: ClassUsage, SecretEmpty: ClassUsage,
	SecretTooLarge: ClassUsage, SecretMismatch: ClassUsage,
	BackendNotSupported: ClassUsage, LocalPathInvalid: ClassUsage, RevealRequired: ClassUsage,
	RepositoryConflict:   ClassExists,
	ConfigLocked:         ClassTemporary,
	ConfigWrite:          ClassWrite,
	ServiceRestartFailed: ClassAgentError,

	AddressInvalid: ClassUsage, SecretInvalid: ClassUsage, S3KeyRejected: ClassUsage,
	StorageAccessDenied: ClassUsage, BucketNotFound: ClassUsage,

	SSHClientMissing: ClassAgentError, SSHClientFailed: ClassAgentError,
	SSHHomeInvalid: ClassUsage, SSHFileRejected: ClassUsage, SSHKeyNotAuthorized: ClassUsage, HostKeyUnconfirmed: ClassUsage,
	HostKeyMismatch: ClassTrust, HostKeyRejected: ClassTrust, HostKeyChanged: ClassTrust,
}

// Failure is a refusal the operator is told about.
type Failure struct {
	Reason Reason
	Class  Class
	// Detail is the explanation, without secrets.
	Detail string
	// ID is the id of the repository that already exists, if known.
	ID string
}

// Fail is a refusal with the class of its reason.
func Fail(reason Reason, format string, args ...any) *Failure {
	return &Failure{Reason: reason, Class: classes[reason], Detail: fmt.Sprintf(format, args...)}
}

// Error is "REASON: detail"; A1's texts stand alone, as they do at start,
// and so does the text of a failure that has no reason (a config error).
func (f *Failure) Error() string {
	if f.Reason == SecretFileRejected || f.Reason == "" {
		return f.Detail
	}
	return string(f.Reason) + ": " + f.Detail
}

// ClassOf is the class of a reason; 0 for one that has none.
func ClassOf(r Reason) Class { return classes[r] }
