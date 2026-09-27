// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package session serves the command stream the agent opened: sends Hello
// and heartbeats, receives RunStep/CancelStep and reports progress, logs
// and results.
package session

import (
	"context"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// Session serves one Connect stream until ctx ends or the stream fails.
type Session interface {
	Serve(ctx context.Context, stream agentv1.AgentService_ConnectClient) error
}

// Stub is the placeholder Session.
type Stub struct{}

// Serve will run the Hello/heartbeat/command loop (roadmap: agent enrollment, stage 1).
func (Stub) Serve(context.Context, agentv1.AgentService_ConnectClient) error {
	return sdk.ErrNotImplemented
}
