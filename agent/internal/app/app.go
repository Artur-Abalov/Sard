// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package app wires the agent's parts together and runs its lifecycle.
package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/secretbind"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
	"google.golang.org/protobuf/types/known/durationpb"
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
	// Bindings are the bindings of the secrets of Local (S11), told to the
	// server by name and target so that the console warns before a step
	// fails; nil reports every secret as unbound.
	Bindings Bindings
	// RestoreDir is restore.dir of the host: the only place the console may
	// restore to. It is told to the server for display and cannot be set by it.
	RestoreDir string
	// RepositoryID returns restic's id of a local repository. The server
	// counts hosts per id to warn when a key exists on one host only.
	RepositoryID func(ctx context.Context, r config.Repository) (string, error)
	// Announced, if not nil, gets the repositories every Register announces
	// (names and ids), for the record sard-agent repo check compares with.
	Announced func(repos []*agentv1.RepositoryInfo)
	// RecoveryKey says whether the repository has a recovery key, by reading
	// its keys (restic key list). Its error text must be scrubbed already.
	// Nil, or an error, leaves the flag unknown.
	RecoveryKey func(ctx context.Context, r config.Repository) (agentv1.RecoveryKeyState, error)
	// Logger receives the WARN line of a repository whose keys could not be
	// read; nil discards.
	Logger *slog.Logger
	// Verifier, set on a verifier, measures what it offers restore
	// verification (V5); nil reports a status that knows nothing.
	Verifier func(ctx context.Context) *agentv1.VerifierStatus
	// VerifierChanged, set on a verifier, is its status when it changed since
	// it was last reported, for a heartbeat; nil when it did not.
	VerifierChanged func(ctx context.Context) *agentv1.VerifierStatus
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
		Secrets:         a.secretInfos(),
		ScriptNames:     a.Local.ScriptNames(),
		// An empty status still tells the server this agent speaks the
		// verification contract; a verifier measures its own (V5).
		Verifier: &agentv1.VerifierStatus{},
	}
	if a.Verifier != nil {
		req.Verifier = a.Verifier(ctx)
	}
	if a.Announced != nil {
		a.Announced(req.Repositories)
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

// Bindings are the bindings of the secrets of the host (*secretbind.Store).
type Bindings interface {
	Binding(name string) (b secretbind.Binding, ok bool, err error)
}

// secretInfos are the secrets of the host with their bindings: names and
// targets, never a value, a path or the CA file.
func (a *Agent) secretInfos() []*agentv1.SecretInfo {
	names := a.Local.SecretNames()
	infos := make([]*agentv1.SecretInfo, 0, len(names))
	for _, name := range names {
		infos = append(infos, a.secretInfo(name))
	}
	return infos
}

func (a *Agent) secretInfo(name string) *agentv1.SecretInfo {
	info := &agentv1.SecretInfo{Name: name, BindingProblem: "the secret has no binding on the agent host and is given to no plugin"}
	if a.Bindings == nil {
		return info
	}
	b, ok, err := a.Bindings.Binding(name)
	switch {
	case err != nil:
		info.BindingProblem = err.Error()
	case ok:
		info.BindingProblem, info.Plugin, info.Auth = "", b.Plugin, b.Auth
		info.AllowTls, info.AllowNoChannelBinding = b.AllowTLS, b.AllowNoChannelBinding
		for _, t := range b.Targets {
			info.Targets = append(info.Targets, t.String())
		}
	}
	return info
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
			RecoveryKey:    a.recoveryKey(ctx, r, id),
			RetentionFloor: floorInfo(a.Local.FloorOf(r.Name)),
		})
	}
	return infos
}

// floorInfo is the retention floor of a repository as the server is told.
func floorInfo(f config.Floor) *agentv1.RetentionFloor {
	return &agentv1.RetentionFloor{
		Disabled:   f.Disabled,
		KeepLast:   f.KeepLast,
		KeepWithin: durationpb.New(time.Duration(f.KeepWithinDays) * 24 * time.Hour),
		Origin:     floorOrigins[f.Origin],
	}
}

var floorOrigins = map[config.FloorOrigin]agentv1.RetentionFloorOrigin{
	config.FloorBuiltIn:          agentv1.RetentionFloorOrigin_RETENTION_FLOOR_ORIGIN_BUILT_IN,
	config.FloorConfigDefault:    agentv1.RetentionFloorOrigin_RETENTION_FLOOR_ORIGIN_CONFIG_DEFAULT,
	config.FloorConfigRepository: agentv1.RetentionFloorOrigin_RETENTION_FLOOR_ORIGIN_CONFIG_REPOSITORY,
}

// recoveryKey is read at every Register and never kept. A repository whose id
// is not known yet is not opened, and one whose keys cannot be read is unknown
// (K21): the registration goes on either way.
func (a *Agent) recoveryKey(ctx context.Context, r config.Repository, id string) agentv1.RecoveryKeyState {
	if id == "" || a.RecoveryKey == nil {
		return agentv1.RecoveryKeyState_RECOVERY_KEY_STATE_UNSPECIFIED
	}
	state, err := a.RecoveryKey(ctx, r)
	if err != nil {
		if a.Logger != nil {
			a.Logger.Warn("cannot read the keys of the repository", "repository", r.Name, "reason", err.Error())
		}
		return agentv1.RecoveryKeyState_RECOVERY_KEY_STATE_UNSPECIFIED
	}
	return state
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
