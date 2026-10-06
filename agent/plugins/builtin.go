// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package plugins lists the source plugins compiled into sard-agent.
package plugins

import (
	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/plugins/files"
	"github.com/Artur-Abalov/sard/agent/plugins/mysql"
	"github.com/Artur-Abalov/sard/agent/plugins/network"
	"github.com/Artur-Abalov/sard/agent/plugins/postgresql"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// Builtin returns the plugins compiled into the agent; they share the
// agent's version. A build with the "e2e" tag adds the e2e stand's plugins
// (stand_e2e.go); a release build has none (stand.go).
func Builtin(agentVersion string) []sdk.Plugin {
	return append([]sdk.Plugin{
		postgresql.Plugin{AgentVersion: agentVersion},
		mysql.Plugin{AgentVersion: agentVersion},
		files.Plugin{AgentVersion: agentVersion},
		network.Plugin{AgentVersion: agentVersion},
	}, stand(agentVersion)...)
}

// Registry returns a registry of the built-in plugins. It panics on a
// plugin the registry refuses (a duplicate or invalid name, an invalid
// version or schema): that is a programming error, caught by the contract
// test in this package, or an agent version outside the server's format.
func Registry(agentVersion string) *sdk.Registry {
	r, err := sdk.NewRegistry(Builtin(agentVersion)...)
	if err != nil {
		panic(err)
	}
	return r
}

// Handlers adapts the built-in plugins to the executor. It panics when a
// schema does not compile: a programming error, caught by the contract
// test in this package.
func Handlers(r *sdk.Registry, secrets *pluginhost.Secrets, repos pluginhost.Repositories, restoreDir string) *pluginhost.Handlers {
	h, err := pluginhost.NewHandlers(r, secrets, repos, restoreDir)
	if err != nil {
		panic(err)
	}
	return h
}
