// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package plugins lists the source plugins compiled into sard-agent.
package plugins

import (
	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/plugins/files"
	"github.com/Artur-Abalov/sard/agent/plugins/postgresql"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// Option sets what the agent tells the built-in plugins about its host. It
// does not name a plugin: a setting of a plugin is a field of builtinSet.
type Option func(*builtinSet)

// builtinSet are the settings of the built-in plugins, one field per plugin
// that takes any.
type builtinSet struct {
	postgresLockShare *float64
}

// WithPostgresLockShare is restore.postgresql_lock_share of the agent: the share
// of the lock table of the target cluster one pg_restore may take (R2b ВК28).
func WithPostgresLockShare(share float64) Option {
	return func(s *builtinSet) { s.postgresLockShare = &share }
}

// Builtin returns the plugins compiled into the agent; they share the
// agent's version. Only plugins that really back up are built in: the
// agent announces every one of them to the server (ADR 0056). A build with
// the "e2e" tag adds the e2e stand's plugins (stand_e2e.go); a release
// build has none (stand.go). The options are the settings of the host that
// reach the plugins; the plugin reads no configuration of the agent itself.
func Builtin(agentVersion string, opts ...Option) []sdk.Plugin {
	var set builtinSet
	for _, opt := range opts {
		opt(&set)
	}
	pg := postgresql.Plugin{AgentVersion: agentVersion, LockShare: set.postgresLockShare}
	return append([]sdk.Plugin{pg, files.Plugin{AgentVersion: agentVersion}}, stand(agentVersion)...)
}

// Registry returns a registry of the built-in plugins. It panics on a
// plugin the registry refuses (a duplicate or invalid name, an invalid
// version or schema): that is a programming error, caught by the contract
// test in this package, or an agent version outside the server's format.
func Registry(agentVersion string, opts ...Option) *sdk.Registry {
	r, err := sdk.NewRegistry(Builtin(agentVersion, opts...)...)
	if err != nil {
		panic(err)
	}
	return r
}

// Handlers adapts the built-in plugins to the executor. It panics when a
// schema does not compile: a programming error, caught by the contract
// test in this package.
func Handlers(r *sdk.Registry, secrets *pluginhost.Secrets, repos pluginhost.Repositories, restoreDir string, opts ...pluginhost.Option) *pluginhost.Handlers {
	h, err := pluginhost.NewHandlers(r, secrets, repos, restoreDir, opts...)
	if err != nil {
		panic(err)
	}
	return h
}
