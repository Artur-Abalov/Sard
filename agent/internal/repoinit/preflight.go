// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoinit

import (
	"errors"
	"io/fs"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/crypto"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/secrets"
)

// Host is how the local checks look at the host's files.
type Host struct {
	UID      uint32
	Stat     secrets.StatFunc
	ReadFile func(name string) ([]byte, error)
}

// Checked is what the local checks of one repository found out.
type Checked struct {
	// EnvAssignments are the KEY=VALUE lines of its env_file.
	EnvAssignments []string
	// PasswordMissing: password_file does not exist (allowed to be created).
	PasswordMissing bool
}

// Preflight runs the checks that come before the backend (С5, С3):
// crypto_provider, then the password file and the env file of this
// repository only. index is the repository's position in the config, as
// in A1's messages. With createPassword a missing password file passes.
func Preflight(h Host, repo config.Repository, index int, createPassword bool) (Checked, *Failure) {
	if f := CheckCryptoProvider(repo); f != nil {
		return Checked{}, f
	}
	missing, f := checkPasswordFile(h, repo, index, createPassword)
	if f != nil {
		return Checked{}, f
	}
	env, f := checkEnvFile(h, repo, index)
	return Checked{EnvAssignments: env, PasswordMissing: missing}, f
}

// CheckCryptoProvider refuses a repository whose crypto_provider is
// anything but empty or the built-in restic encryption (stage 1).
func CheckCryptoProvider(repo config.Repository) *Failure {
	if p := repo.CryptoProvider; p != "" && p != crypto.ResticAESName {
		return fail(CryptoProviderUnsupported, "crypto_provider %q of repository %q is not supported; stage 1 supports only the built-in restic encryption (%s, or leave crypto_provider empty)", p, repo.Name, crypto.ResticAESName)
	}
	return nil
}

func rejected(err error) *Failure {
	return &Failure{Reason: SecretFileRejected, Class: refusal.ClassOf(SecretFileRejected), Detail: err.Error()}
}

func checkPasswordFile(h Host, repo config.Repository, index int, createPassword bool) (missing bool, f *Failure) {
	key := secrets.RepositoryKey(index, repo, "password_file")
	if err := secrets.CheckFile(key, repo.PasswordFile, h.UID, h.Stat); err != nil {
		return false, rejected(err)
	}
	return passwordFileState(h, repo, createPassword)
}

// passwordFileState: the file exists and is not empty, or is to be created.
func passwordFileState(h Host, repo config.Repository, createPassword bool) (missing bool, f *Failure) {
	info, err := h.Stat(repo.PasswordFile)
	switch {
	case errors.Is(err, fs.ErrNotExist) && createPassword:
		return true, nil
	case err != nil:
		return false, fail(PasswordFileMissing, "password_file %s of repository %q does not exist; create it, or run `sard-agent repo init --generate-password %s` to have the command create it", repo.PasswordFile, repo.Name, repo.Name)
	case info.Size == 0:
		return false, fail(PasswordFileEmpty, "password_file %s of repository %q is empty", repo.PasswordFile, repo.Name)
	}
	return false, nil
}

func checkEnvFile(h Host, repo config.Repository, index int) ([]string, *Failure) {
	if repo.EnvFile == "" {
		return nil, nil
	}
	key := secrets.RepositoryKey(index, repo, "env_file")
	if err := secrets.CheckFile(key, repo.EnvFile, h.UID, h.Stat); err != nil {
		return nil, rejected(err)
	}
	data, err := h.ReadFile(repo.EnvFile)
	if err != nil {
		return nil, fail(EnvFileMissing, "env_file %s of repository %q cannot be read: %v", repo.EnvFile, repo.Name, err)
	}
	env, err := restic.ParseEnvFile(data)
	if err != nil {
		return nil, fail(EnvFileInvalid, "env_file %s of repository %q: %v", repo.EnvFile, repo.Name, err)
	}
	return env, nil
}
