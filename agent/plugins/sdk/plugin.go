// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

// Package sdk defines the contract every Sard source plugin implements.
//
// A backup of one source runs Prepare → Dump → Stream; a restore check runs
// Verify against a restored copy. The package is Apache-2.0 and imports no
// AGPL code, so third-party plugins can depend on it freely.
package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
)

// Config is a plugin configuration document, valid against ConfigSchema.
type Config = json.RawMessage

// Dump describes a consistent snapshot produced by Plugin.Dump.
type Dump struct {
	// Paths are local files or directories that make up the snapshot.
	Paths []string
}

// ErrNotImplemented is returned by plugin methods that are not written yet.
var ErrNotImplemented = errors.New("not implemented")

// Plugin is a backup source: a database, a directory tree, a device config.
type Plugin interface {
	// Name is the unique plugin identifier, e.g. "postgresql".
	Name() string
	// ConfigSchema returns the JSON Schema (draft 2020-12) of Config.
	// The UI renders configuration forms from it.
	ConfigSchema() []byte
	// Prepare runs pre-flight checks and pre-backup hooks.
	Prepare(ctx context.Context, cfg Config) error
	// Dump makes a consistent snapshot of the source.
	Dump(ctx context.Context, cfg Config) (Dump, error)
	// Stream writes the snapshot bytes to w (restic's stdin).
	Stream(ctx context.Context, d Dump, w io.Writer) error
	// Verify checks a restored copy located at restoredPath.
	Verify(ctx context.Context, cfg Config, restoredPath string) error
}
