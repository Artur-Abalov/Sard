// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package transport is the agent's gRPC client to sard-server. The agent
// always dials out; it never opens a listening port.
package transport

import (
	"context"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// Client talks to AgentService on the server.
type Client interface {
	Register(ctx context.Context, req *agentv1.RegisterRequest) (*agentv1.RegisterResponse, error)
	// Connect opens the command stream; the server sends commands over it.
	Connect(ctx context.Context) (agentv1.AgentService_ConnectClient, error)
}

// GRPC is the placeholder gRPC client.
type GRPC struct {
	address string
}

// NewGRPC returns a client for the server at address (host:port).
func NewGRPC(address string) *GRPC {
	return &GRPC{address: address}
}

// Address is the server the client dials.
func (c *GRPC) Address() string { return c.address }

// Register will dial the server over mTLS (roadmap: agent enrollment, stage 1; ADR 0009).
func (*GRPC) Register(context.Context, *agentv1.RegisterRequest) (*agentv1.RegisterResponse, error) {
	return nil, sdk.ErrNotImplemented
}

// Connect will open the bidirectional command stream (roadmap: agent enrollment, stage 1).
func (*GRPC) Connect(context.Context) (agentv1.AgentService_ConnectClient, error) {
	return nil, sdk.ErrNotImplemented
}
