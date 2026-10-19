// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore

import (
	"context"
	"io"
	"io/fs"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// DatabaseRepository is what the restore of a database needs of a restic
// repository: the list of snapshots and the content of a file of one.
type DatabaseRepository interface {
	Snapshots(ctx context.Context, tags []string) ([]restic.Snapshot, error)
	Dump(ctx context.Context, snapshotID, path string, w io.Writer) error
}

// ToolFS is the part of the file system the engine looks at to find the
// programs of PostgreSQL.
type ToolFS interface {
	Stat(name string) (fs.FileInfo, error)
}

// PostgresOptions configure the restore of a database. The agent has no
// PostgreSQL driver: like the plugin, it runs psql and pg_restore.
type PostgresOptions struct {
	// Exec starts psql and pg_restore.
	Exec restic.Executor
	// Tools finds the programs.
	Tools ToolFS
	// Environ is the environment of the agent, from which the programs get
	// their PATH and HOME (without its PG* variables and locale).
	Environ func() []string
	// StateDir is executor.state_dir: the rollback journal lives below it.
	StateDir string
	// LockShare is restore.postgresql_lock_share (ВК28).
	LockShare float64
	// Secret reads a secret of the host by name: the start needs the password
	// of an interrupted restore to roll it back.
	Secret func(name string) ([]byte, error)
}

// DatabaseRequest is a restore of a snapshot of a database into a new one.
type DatabaseRequest struct {
	CommandID  string
	Repository string
	SnapshotID string
	// Source is the sard.source of the step: the snapshot must carry it.
	Source string
	Plan   sdk.DatabaseRestore
	// Password is the content of the secret Plan.PasswordRef.
	Password []byte
}

// DatabaseOutput is what a restore of a database produced.
type DatabaseOutput struct {
	Database string
	Tables   uint64
	Roles    uint64
}
