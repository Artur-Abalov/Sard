// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package crypto hands repository keys to restic, which performs the
// encryption itself. See docs/adr/0008-crypto-provider.md.
package crypto

import (
	"context"
	"errors"
)

// Key is what restic needs to open a repository.
type Key struct {
	// Env is added to the environment of the restic process,
	// e.g. "RESTIC_PASSWORD_FILE=/etc/sard/restic.pass".
	Env []string
}

// Provider supplies repository keys. The rest of the agent depends on
// this interface only.
type Provider interface {
	// Name identifies the provider in logs and reports.
	Name() string
	// RepositoryKey returns the key for the given repository.
	RepositoryKey(ctx context.Context, repository string) (Key, error)
}

// ErrNoPasswordFile is returned when resticAES has no password file.
var ErrNoPasswordFile = errors.New("restic password file is not configured")

type resticAES struct {
	passwordFile string
}

// NewResticAES returns the provider for restic's built-in AES-256 encryption.
// The password file is passed to restic by path and never read by the agent.
func NewResticAES(passwordFile string) Provider {
	return resticAES{passwordFile: passwordFile}
}

func (resticAES) Name() string { return "restic-aes" }

func (p resticAES) RepositoryKey(_ context.Context, _ string) (Key, error) {
	if p.passwordFile == "" {
		return Key{}, ErrNoPasswordFile
	}
	return Key{Env: []string{"RESTIC_PASSWORD_FILE=" + p.passwordFile}}, nil
}
