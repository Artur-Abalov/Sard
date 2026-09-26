// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package app wires the agent's parts together and runs its lifecycle.
package app

import (
	"context"
	"fmt"

	"github.com/Artur-Abalov/sard/agent/internal/transport"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// Agent is the running agent.
type Agent struct {
	Client   transport.Client
	Plugins  *sdk.Registry
	Hostname string
	Version  string
}

// Run registers with the server and serves commands until ctx ends.
func (a *Agent) Run(ctx context.Context) error {
	if _, err := a.Client.Register(ctx, a.registerRequest()); err != nil {
		return fmt.Errorf("register: %w", err)
	}
	if _, err := a.Client.Connect(ctx); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	return nil
}

func (a *Agent) registerRequest() *agentv1.RegisterRequest {
	req := &agentv1.RegisterRequest{Hostname: a.Hostname, AgentVersion: a.Version}
	for _, name := range a.Plugins.Names() {
		p, _ := a.Plugins.Get(name)
		req.Plugins = append(req.Plugins, &agentv1.Plugin{Name: name, ConfigSchema: string(p.ConfigSchema())})
	}
	return req
}
