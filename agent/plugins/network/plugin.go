// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package network is the network device configuration (MikroTik, Eltex) over SSH source plugin.
package network

import (
	"context"
	_ "embed"
	"io"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

//go:embed schema.json
var schema []byte

// Plugin backs up network device configuration (MikroTik, Eltex) over SSH.
// Restore verification will check that the restored configuration parses (roadmap: restore verification, stage 2).
type Plugin struct {
	// AgentVersion is the version of sard-agent, which ships the plugin.
	AgentVersion string
}

var _ sdk.Plugin = Plugin{}

// Name implements sdk.Plugin.
func (Plugin) Name() string { return "network" }

// Version implements sdk.Plugin: the plugin ships with the agent.
func (p Plugin) Version() string { return p.AgentVersion }

// ConfigSchema implements sdk.Plugin.
func (Plugin) ConfigSchema() []byte { return schema }

// Prepare will check SSH reachability (roadmap: network devices, stage 3).
func (Plugin) Prepare(context.Context, sdk.Host, sdk.Config) error { return sdk.ErrNotImplemented }

// Dump will export the running configuration over SSH (roadmap: network devices, stage 3).
func (Plugin) Dump(context.Context, sdk.Host, sdk.Config) (sdk.Dump, error) {
	return sdk.Dump{}, sdk.ErrNotImplemented
}

// Stream will pipe the dump into restic (roadmap: network devices, stage 3).
func (Plugin) Stream(context.Context, sdk.Host, sdk.Config, sdk.Dump, io.Writer) error {
	return sdk.ErrNotImplemented
}
