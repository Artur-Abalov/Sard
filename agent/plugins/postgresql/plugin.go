// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package postgresql is the PostgreSQL source plugin: a logical dump of one
// database (pg_dump, custom format) streamed into restic, with the global
// objects of the cluster (pg_dumpall --globals-only) as a second snapshot.
// The tools are the host's own (see docs/plugins/postgresql.md).
package postgresql

import (
	_ "embed"
	"io/fs"
	"os"
	"time"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

//go:embed schema.json
var schema []byte

// Plugin backs up one PostgreSQL database and restores it into a new one; it
// also plans the files of a snapshot for "sard-agent repo restore" and looks for
// the client programs on the host.
// Each backup carries a fingerprint of the database taken in the snapshot
// pg_dump dumps (fingerprint.go, session.go).
type Plugin struct {
	// AgentVersion is the version of sard-agent, which ships the plugin.
	AgentVersion string
	// Runner starts psql, pg_dump and pg_dumpall; nil starts host processes.
	Runner Runner
	// FS looks for the tools; nil is the host's file system.
	FS FS
	// Environ is the environment of the agent, from which the tools get
	// their PATH and HOME; nil is os.Environ.
	Environ func() []string
	// LockShare is the share of the lock table of the target cluster a restore
	// may take for one transaction of pg_restore (R2b ВК28); nil is DefaultLockShare.
	// The agent sets it from its configuration.
	LockShare *float64
	// CountTimeout bounds the count of the rows of one key table, a minute
	// when zero; CountBudget all of them, ten minutes when zero (V2).
	CountTimeout, CountBudget time.Duration
	// Now is the clock of CountBudget; nil is time.Now.
	Now func() time.Time
}

var _ sdk.Plugin = Plugin{}

// FS is the part of the file system the plugin looks at.
type FS interface {
	Stat(name string) (fs.FileInfo, error)
}

type osFS struct{}

func (osFS) Stat(name string) (fs.FileInfo, error) { return os.Stat(name) }

// Name implements sdk.Plugin.
func (Plugin) Name() string { return "postgresql" }

// Version implements sdk.Plugin: the plugin ships with the agent.
func (p Plugin) Version() string { return p.AgentVersion }

// ConfigSchema implements sdk.Plugin.
func (Plugin) ConfigSchema() []byte { return schema }
