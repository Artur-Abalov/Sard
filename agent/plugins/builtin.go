// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package plugins lists the source plugins compiled into sard-agent.
package plugins

import (
	"github.com/Artur-Abalov/sard/agent/plugins/files"
	"github.com/Artur-Abalov/sard/agent/plugins/mysql"
	"github.com/Artur-Abalov/sard/agent/plugins/network"
	"github.com/Artur-Abalov/sard/agent/plugins/postgresql"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// Builtin returns the plugins compiled into the agent.
func Builtin() []sdk.Plugin {
	return []sdk.Plugin{postgresql.Plugin{}, mysql.Plugin{}, files.Plugin{}, network.Plugin{}}
}

// Registry returns a registry of the built-in plugins. It panics on a
// duplicate or empty name: that is a programming error, caught by the
// contract test in this package.
func Registry() *sdk.Registry {
	r, err := sdk.NewRegistry(Builtin()...)
	if err != nil {
		panic(err)
	}
	return r
}
