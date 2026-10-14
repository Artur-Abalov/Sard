// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package transport

import (
	"context"
	"time"

	"google.golang.org/grpc/keepalive"

	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// Internals exposed to the external tests of this package.

func KeepaliveForTest() keepalive.ClientParameters { return keepaliveParams() }

func (t *Transport) UsesRealClockForTest() bool {
	_, ok := t.opts.Clock.(realClock)
	return ok
}

func (t *Transport) LogQueueForTest() int { return t.out.logCap }

// ServeForTest runs one stream over a client the test supplies.
func (t *Transport) ServeForTest(ctx context.Context, client agentv1.AgentServiceClient, interval time.Duration) error {
	return t.serve(ctx, client, interval)
}

// SendForTest runs the sender over a stream the test supplies.
func (t *Transport) SendForTest(ctx context.Context, stream agentv1.AgentService_ConnectClient, interval time.Duration) error {
	return t.send(ctx, stream, interval)
}
