// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package app wires the agent's parts together and runs its lifecycle.
package app

import (
	"context"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// ProtocolVersion is the version of the agent contract this agent speaks.
const ProtocolVersion = 1

// Link is the connection to the server (transport.Transport).
type Link interface {
	Run(ctx context.Context) error
}

// Agent is the running agent.
type Agent struct {
	Link    Link
	Plugins *sdk.Registry
	// Handlers run the plugins' steps; their actions are announced.
	Handlers executor.Registry
	Hostname string
	// MachineID is the HMAC of the host's machine-id (package machineid),
	// computed once at start and sent in every Register. The agent never
	// sends its role: the server reads it from the certificate.
	MachineID string
	Version   string
	// OS and Arch are runtime.GOOS and runtime.GOARCH in production.
	OS   string
	Arch string
	// Local holds the host-local repositories, secrets and scripts. Only
	// their names and metadata are sent to the server (ADR 0008).
	Local config.Config
	// RestoreDir is restore.dir of the host: the only place the console may
	// restore to. It is told to the server for display and cannot be set by it.
	RestoreDir string
	// RepositoryID returns restic's id of a local repository. The server
	// counts hosts per id to warn when a key exists on one host only.
	RepositoryID func(ctx context.Context, r config.Repository) (string, error)
}

// Run keeps the agent connected to the server until ctx ends or the
// server refuses the agent for good.
func (a *Agent) Run(ctx context.Context) error {
	return a.Link.Run(ctx)
}

// RegisterRequest describes this host to the server; the transport sends
// it before every stream.
func (a *Agent) RegisterRequest(ctx context.Context) *agentv1.RegisterRequest {
	req := &agentv1.RegisterRequest{
		Hostname:        a.Hostname,
		MachineId:       a.MachineID,
		AgentVersion:    a.Version,
		ProtocolVersion: ProtocolVersion,
		Os:              a.OS,
		Arch:            a.Arch,
		Repositories:    a.repositoryInfos(ctx),
		RestoreDir:      a.RestoreDir,
		SecretNames:     a.Local.SecretNames(),
		ScriptNames:     a.Local.ScriptNames(),
	}
	for _, name := range a.Plugins.Names() {
		p, _ := a.Plugins.Get(name)
		req.Plugins = append(req.Plugins, &agentv1.Plugin{
			Name:                name,
			ConfigSchema:        string(p.ConfigSchema()),
			Version:             p.Version(),
			Actions:             a.actions(name),
			RestoreConfigSchema: restoreSchema(p),
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

// actions are what the plugin's handler runs; none without a handler.
func (a *Agent) actions(plugin string) []agentv1.Action {
	h, ok := a.Handlers.Handler(plugin)
	if !ok {
		return nil
	}
	return h.Actions()
}

// restoreSchema is the schema of the restore config of a plugin that the
// console can restore; empty for the others.
func restoreSchema(p sdk.Plugin) string {
	if r, ok := p.(sdk.Restorer); ok {
		return string(r.RestoreConfigSchema())
	}
	return ""
}
