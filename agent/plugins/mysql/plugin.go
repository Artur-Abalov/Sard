// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package mysql is the MySQL source plugin.
package mysql

import (
	"context"
	_ "embed"
	"io"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

//go:embed schema.json
var schema []byte

// Plugin backs up MySQL.
// Restore verification will start the restored dump in a sandbox container and run check queries (roadmap: restore verification, stage 2).
type Plugin struct {
	// AgentVersion is the version of sard-agent, which ships the plugin.
	AgentVersion string
}

var _ sdk.Plugin = Plugin{}

// Name implements sdk.Plugin.
func (Plugin) Name() string { return "mysql" }

// Version implements sdk.Plugin: the plugin ships with the agent.
func (p Plugin) Version() string { return p.AgentVersion }

// ConfigSchema implements sdk.Plugin.
func (Plugin) ConfigSchema() []byte { return schema }

// Prepare will check connectivity and run pre-backup hooks (roadmap: first sources, stage 1).
func (Plugin) Prepare(context.Context, sdk.Host, sdk.Config) error { return sdk.ErrNotImplemented }

// Dump will run mysqldump (roadmap: first sources, stage 1).
func (Plugin) Dump(context.Context, sdk.Host, sdk.Config) (sdk.Dump, error) {
	return sdk.Dump{}, sdk.ErrNotImplemented
}

// Stream will pipe the dump into restic (roadmap: first sources, stage 1).
func (Plugin) Stream(context.Context, sdk.Host, sdk.Config, sdk.Dump, io.Writer) error {
	return sdk.ErrNotImplemented
}
