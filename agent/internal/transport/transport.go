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

// Enroller talks to EnrollmentService: server-side TLS only, before the
// agent has a client certificate.
type Enroller interface {
	Enroll(ctx context.Context, req *agentv1.EnrollRequest) (*agentv1.EnrollResponse, error)
}

// Client talks to AgentService on the server over mTLS.
type Client interface {
	RenewCertificate(ctx context.Context, req *agentv1.RenewCertificateRequest) (*agentv1.RenewCertificateResponse, error)
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

// Enroll will exchange an enrollment token and a CSR for a client certificate (roadmap: agent enrollment, stage 1; ADR 0009).
func (*GRPC) Enroll(context.Context, *agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
	return nil, sdk.ErrNotImplemented
}

// RenewCertificate will rotate the client certificate before it expires (roadmap: agent enrollment, stage 1).
func (*GRPC) RenewCertificate(context.Context, *agentv1.RenewCertificateRequest) (*agentv1.RenewCertificateResponse, error) {
	return nil, sdk.ErrNotImplemented
}

// Register will dial the server over mTLS (roadmap: agent enrollment, stage 1; ADR 0009).
func (*GRPC) Register(context.Context, *agentv1.RegisterRequest) (*agentv1.RegisterResponse, error) {
	return nil, sdk.ErrNotImplemented
}

// Connect will open the bidirectional command stream (roadmap: agent enrollment, stage 1).
func (*GRPC) Connect(context.Context) (agentv1.AgentService_ConnectClient, error) {
	return nil, sdk.ErrNotImplemented
}
