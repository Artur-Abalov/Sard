// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package transport_test

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Artur-Abalov/sard/agent/internal/transport"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// LogQueue: positive values are kept, zero or less means 1024 lines.
func TestLogQueueDefaultsTo1024(t *testing.T) {
	ca := newAuthority(t)
	files := ca.agentFiles(t, ca)
	for _, tc := range []struct{ set, want int }{{0, 1024}, {-1, 1024}, {-100, 1024}, {1, 1}, {2, 2}, {4096, 4096}} {
		tr, err := transport.New(transport.Options{Address: "h:1", TLS: files, LogQueue: tc.set})
		if err != nil {
			t.Fatal(err)
		}
		if got := tr.LogQueueForTest(); got != tc.want {
			t.Errorf("LogQueue %d: queue = %d, want %d", tc.set, got, tc.want)
		}
	}
}

// Without Options.Rand the backoff still has jitter: a delay within the
// first step of one second.
func TestBackoffWithoutARandSourceStaysWithinTheBound(t *testing.T) {
	r := newRig(t)
	r.server.register = func() (*agentv1.RegisterResponse, error) { return nil, status.Error(codes.Unavailable, "starting") }
	tr, err := transport.New(transport.Options{Address: r.server.addr, TLS: r.ca.agentFiles(t, r.ca), Clock: r.clock, Commands: r.commands, State: r.state,
		Register: func(context.Context) *agentv1.RegisterRequest { return &agentv1.RegisterRequest{} }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- tr.Run(ctx) }()
	if d := r.clock.waitTimer(t); d < 0 || d >= time.Second {
		t.Errorf("delay = %v, want [0, 1s)", d)
	}
	cancel()
	_ = wait(t, done)
}
