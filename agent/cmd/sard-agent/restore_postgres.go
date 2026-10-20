// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/consolerestore"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// postgresOptions are what the restore of a database from the console needs
// of this host (R2b): the programs of PostgreSQL as processes of the service,
// the rollback journal in the state directory of the executor, and the secrets
// of the host to sign in again at a start that finds an interrupted restore.
func postgresOptions(cfg config.Config, readFile func(string) ([]byte, error)) consolerestore.PostgresOptions {
	return consolerestore.PostgresOptions{
		Exec:      restic.ProcessExecutor{},
		Tools:     osTools{},
		Environ:   os.Environ,
		StateDir:  executorStateDir(cfg.Executor.StateDir),
		LockShare: cfg.PostgresqlLockShare(),
		Secret: func(name string) ([]byte, error) {
			path, ok := cfg.Secrets[name]
			if !ok {
				return nil, fmt.Errorf("secret %q is not defined on this host", name)
			}
			data, err := readFile(path)
			if err != nil {
				return nil, fmt.Errorf("secret %q: %w", name, withoutPath(err))
			}
			return data, nil
		},
	}
}

// osTools looks at the file system of the host for the programs of PostgreSQL.
type osTools struct{}

func (osTools) Stat(name string) (fs.FileInfo, error) { return os.Stat(filepath.Clean(name)) }

// withoutPath drops the path from a file error: a path leaves the host in no
// message (ADR 0008).
func withoutPath(err error) error {
	if pe, ok := err.(*fs.PathError); ok {
		return pe.Err
	}
	return err
}
