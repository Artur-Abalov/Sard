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
	// ID returns restic's repository id (from the repository config). It is
	// not a secret: the server uses it to see how many hosts hold the key.
	ID(ctx context.Context) (string, error)
	// Backup stores the bytes read from src as a new snapshot and returns its id.
	Backup(ctx context.Context, src io.Reader, tags []string) (string, error)
	// Restore writes snapshot snapshotID into the target directory.
	Restore(ctx context.Context, snapshotID, target string) error
}

// CLI runs the restic binary. The repository key comes from a crypto.Provider.
type CLI struct {
	binary string
	name   string // repository name on this host; the key is looked up by it
	url    string
	keys   crypto.Provider
}

// New returns a Repository backed by the restic binary.
func New(binary, name, url string, keys crypto.Provider) *CLI {
	return &CLI{binary: binary, name: name, url: url, keys: keys}
}

// ID will run `restic cat config` and return its id (roadmap: first backup, stage 1).
func (*CLI) ID(context.Context) (string, error) {
	return "", sdk.ErrNotImplemented
}

// Backup will run `restic backup --stdin` (roadmap: first backup, stage 1).
func (*CLI) Backup(context.Context, io.Reader, []string) (string, error) {
	return "", sdk.ErrNotImplemented
}

// Restore will run `restic restore` (roadmap: restore verification, stage 2).
func (*CLI) Restore(context.Context, string, string) error {
	return sdk.ErrNotImplemented
}
