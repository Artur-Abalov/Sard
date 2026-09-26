// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package restic wraps the restic binary, which stores, deduplicates and
// encrypts backup data.
package restic

import (
	"context"
	"io"

	"github.com/Artur-Abalov/sard/agent/internal/crypto"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// Repository is a restic repository on S3, SFTP or local disk.
type Repository interface {
	// Backup stores the bytes read from src as a new snapshot and returns its id.
	Backup(ctx context.Context, src io.Reader, tags []string) (string, error)
	// Restore writes snapshot snapshotID into the target directory.
	Restore(ctx context.Context, snapshotID, target string) error
}

// CLI runs the restic binary. The repository key comes from a crypto.Provider.
type CLI struct {
	binary     string
	repository string
	keys       crypto.Provider
}

// New returns a Repository backed by the restic binary.
func New(binary, repository string, keys crypto.Provider) *CLI {
	return &CLI{binary: binary, repository: repository, keys: keys}
}

// Backup will run `restic backup --stdin` (roadmap: first backup, stage 1).
func (*CLI) Backup(context.Context, io.Reader, []string) (string, error) {
	return "", sdk.ErrNotImplemented
}

// Restore will run `restic restore` (roadmap: restore verification, stage 2).
func (*CLI) Restore(context.Context, string, string) error {
	return sdk.ErrNotImplemented
}
