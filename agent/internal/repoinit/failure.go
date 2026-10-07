// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoinit

import (
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
)

// Reason, Class and Failure are the refusal package's: repoinit keeps the
// names its callers always used.
type (
	Reason  = refusal.Reason
	Class   = refusal.Class
	Failure = refusal.Failure
)

// The reasons, as the refusal package names them.
const (
	RepositoryUnknown         = refusal.RepositoryUnknown
	CryptoProviderUnsupported = refusal.CryptoProviderUnsupported
	PasswordFileMissing       = refusal.PasswordFileMissing
	PasswordFileEmpty         = refusal.PasswordFileEmpty
	EnvFileMissing            = refusal.EnvFileMissing
	EnvFileInvalid            = refusal.EnvFileInvalid
	SecretFileRejected        = refusal.SecretFileRejected
	ResticNotFound            = refusal.ResticNotFound
	ResticTooOld              = refusal.ResticTooOld
	ResticUnusable            = refusal.ResticUnusable
	ResticOutputUnexpected    = refusal.ResticOutputUnexpected
	RepositoryExists          = refusal.RepositoryExists
	WrongPassword             = refusal.WrongPassword
	BackendUnavailable        = refusal.BackendUnavailable
	BackendRefused            = refusal.BackendRefused
	Interrupted               = refusal.Interrupted
	Timeout                   = refusal.Timeout
	InitInProgress            = refusal.InitInProgress
	PasswordFileWrite         = refusal.PasswordFileWrite
	LockWrite                 = refusal.LockWrite
	PrivilegesRequired        = refusal.PrivilegesRequired
	ServiceUserUnknown        = refusal.ServiceUserUnknown
	NameInvalid               = refusal.NameInvalid
	DefinedInConfig           = refusal.DefinedInConfig
	PathInUse                 = refusal.PathInUse
	SecretSourceMissing       = refusal.SecretSourceMissing
	SecretSourceConflict      = refusal.SecretSourceConflict
	SecretEmpty               = refusal.SecretEmpty
	SecretTooLarge            = refusal.SecretTooLarge
	SecretMismatch            = refusal.SecretMismatch
	BackendNotSupported       = refusal.BackendNotSupported
	LocalPathInvalid          = refusal.LocalPathInvalid
	RevealRequired            = refusal.RevealRequired
	RepositoryConflict        = refusal.RepositoryConflict
	ConfigLocked              = refusal.ConfigLocked
	ConfigWrite               = refusal.ConfigWrite
	ServiceRestartFailed      = refusal.ServiceRestartFailed
)

// The classes.
const (
	ClassAgentError = refusal.ClassAgentError
	ClassUsage      = refusal.ClassUsage
	ClassExists     = refusal.ClassExists
	ClassTemporary  = refusal.ClassTemporary
	ClassWrite      = refusal.ClassWrite
)

func fail(reason Reason, format string, args ...any) *Failure {
	return refusal.Fail(reason, format, args...)
}

// Fail is a refusal with the class of its reason.
func Fail(reason Reason, format string, args ...any) *Failure {
	return refusal.Fail(reason, format, args...)
}

// UnknownRepository is the refusal for a name the config does not have; it
// lists the names that are there.
func UnknownRepository(name, configPath string, known []string) *Failure {
	if len(known) == 0 {
		return fail(RepositoryUnknown, "no repositories configured in %s", configPath)
	}
	return fail(RepositoryUnknown, "no repository named %q in %s; configured repositories: %s", name, configPath, strings.Join(known, ", "))
}
