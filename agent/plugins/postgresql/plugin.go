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

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

//go:embed schema.json
var schema []byte

// Plugin backs up one PostgreSQL database.
// Restore verification will start the restored dump in a sandbox container and run check queries (roadmap: restore verification, stage 2).
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

// RestoreNotImplemented is the message of a restore step: the restore of a
// dump is done by hand for now (F1 ПГ13).
func (Plugin) RestoreNotImplemented() string {
	return "restore for the postgresql plugin is not implemented yet"
}
