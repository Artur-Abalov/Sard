// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package app wires the agent's parts together and runs its lifecycle.
package app

import (
	"context"

	"github.com/Artur-Abalov/sard/agent/internal/config"
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

// Link is the connection to the server (transport.Transport).
type Link interface {
	Connect(ctx context.Context) error
}

// Agent is the running agent.
type Agent struct {
	Link     Link
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

// Run connects to the server and serves commands until ctx ends or the
// connection fails.
func (a *Agent) Run(ctx context.Context) error {
	return a.Link.Connect(ctx)
}

// RegisterRequest describes this host to the server; the transport sends
// it before every stream.
func (a *Agent) RegisterRequest(ctx context.Context) *agentv1.RegisterRequest {
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

// NoExecutor stands in for the command executor (A4) until it is wired in:
// nothing runs, nothing is pending, and commands from the server are dropped.
type NoExecutor struct{}

func (NoExecutor) Submit(*agentv1.RunStep)               {}
func (NoExecutor) Cancel(string)                         {}
func (NoExecutor) Ack(string) error                      { return nil }
func (NoExecutor) RunningIDs() []string                  { return nil }
func (NoExecutor) PendingResults() []*agentv1.StepResult { return nil }
