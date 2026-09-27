// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package crypto hands repository keys to restic, which performs the
// encryption itself. Keys live on the agent host only; the server names a
// repository and never sees its key. See docs/adr/0008-crypto-provider.md.
package crypto

import (
	"context"
	"errors"
	"fmt"
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

// ErrUnknownRepository is returned for a repository not configured on this host.
var ErrUnknownRepository = errors.New("repository is not configured on this host")

type resticAES struct {
	passwordFiles map[string]string
}

// NewResticAES returns the provider for restic's built-in AES-256 encryption.
// passwordFiles maps repository names to password files that stay on this
// host; they are passed to restic by path and never read by the agent.
func NewResticAES(passwordFiles map[string]string) Provider {
	return resticAES{passwordFiles: passwordFiles}
}

func (resticAES) Name() string { return "restic-aes" }

func (p resticAES) RepositoryKey(_ context.Context, repository string) (Key, error) {
	file, ok := p.passwordFiles[repository]
	if !ok {
		return Key{}, fmt.Errorf("%w: %q", ErrUnknownRepository, repository)
	}
	return Key{Env: []string{"RESTIC_PASSWORD_FILE=" + file}}, nil
}
