// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package app wires the agent's parts together and runs its lifecycle.
package app

import (
	"context"
	"fmt"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/transport"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// ProtocolVersion is the version of the agent contract this agent speaks.
const ProtocolVersion = 1

// builtinActions are the actions every built-in plugin exposes through
// sdk.Plugin (prepare → dump → stream for backup and restore, verify).
var builtinActions = []agentv1.Action{
	agentv1.Action_ACTION_BACKUP,
	agentv1.Action_ACTION_RESTORE,
	agentv1.Action_ACTION_VERIFY,
}

// Agent is the running agent.
type Agent struct {
	Client   transport.Client
	Plugins  *sdk.Registry
	Hostname string
	Version  string
	// OS and Arch are runtime.GOOS and runtime.GOARCH in production.
	OS   string
	Arch string
	// Local holds the host-local repositories, secrets and scripts. Only
	// their names and metadata are sent to the server (ADR 0008).
	Local config.Config
	// RepositoryID returns restic's id of a local repository. The server
	// counts hosts per id to warn when a key exists on one host only.
	RepositoryID func(ctx context.Context, r config.Repository) (string, error)
}

// Run registers with the server and serves commands until ctx ends.
func (a *Agent) Run(ctx context.Context) error {
	if _, err := a.Client.Register(ctx, a.registerRequest(ctx)); err != nil {
		return fmt.Errorf("register: %w", err)
	}
	if _, err := a.Client.Connect(ctx); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	return nil
}

func (a *Agent) registerRequest(ctx context.Context) *agentv1.RegisterRequest {
	req := &agentv1.RegisterRequest{
		Hostname:        a.Hostname,
		AgentVersion:    a.Version,
		ProtocolVersion: ProtocolVersion,
		Os:              a.OS,
		Arch:            a.Arch,
		Repositories:    a.repositoryInfos(ctx),
		SecretNames:     a.Local.SecretNames(),
		ScriptNames:     a.Local.ScriptNames(),
	}
	for _, name := range a.Plugins.Names() {
		p, _ := a.Plugins.Get(name)
		req.Plugins = append(req.Plugins, &agentv1.Plugin{
			Name:         name,
			ConfigSchema: string(p.ConfigSchema()),
			// Built-in plugins ship with the agent and share its version.
			Version: a.Version,
			Actions: builtinActions,
		})
	}
	return req
}

// repositoryInfos describes the host's repositories without URL or key.
// A repository whose id cannot be read yet is announced with an empty id.
func (a *Agent) repositoryInfos(ctx context.Context) []*agentv1.RepositoryInfo {
	infos := make([]*agentv1.RepositoryInfo, 0, len(a.Local.Repositories))
	for _, r := range a.Local.Repositories {
		id, _ := a.RepositoryID(ctx, r)
		infos = append(infos, &agentv1.RepositoryInfo{
			Name:           r.Name,
			Backend:        r.Backend(),
			RepositoryId:   id,
			CryptoProvider: r.CryptoProvider,
		})
	}
	return infos
}
