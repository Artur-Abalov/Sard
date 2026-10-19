// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package transport_test

import (
	"context"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/Artur-Abalov/sard/agent/internal/transport"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// docs/specs/agent/agent-certificate-renewal.feature: P17 (renewal starts after
// the first Register) and P23 (CERT_SUPERSEDED).

// hooked is a transport against the rig's server with the given hooks.
func hooked(t *testing.T, r *rig, registered func(), superseded func(context.Context) bool) *transport.Transport {
	t.Helper()
	tr, err := transport.New(transport.Options{
		Address: r.server.addr,
		TLS:     r.ca.agentFiles(t, r.ca),
		Register: func(context.Context) *agentv1.RegisterRequest {
			return &agentv1.RegisterRequest{Hostname: "db1", ProtocolVersion: 1}
		},
		Commands:     r.commands,
		State:        r.state,
		Clock:        r.clock,
		Rand:         func() float64 { return 1 },
		OnRegistered: registered,
		OnSuperseded: superseded,
	})
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func TestTheRegisteredHookRunsAfterASuccessfulRegisterOnly(t *testing.T) {
	r := newRig(t)
	var calls atomic.Int32
	r.server.register = func() (*agentv1.RegisterResponse, error) {
		if r.server.registered == nil || len(r.server.registered) == 1 {
			return nil, reasoned(codes.Unavailable, "down", "sard.dev", "INTERNAL_RETRYABLE", nil)
		}
		return registered("a", beat), nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr := hooked(t, r, func() { calls.Add(1) }, nil)
	done := make(chan error, 1)
	go func() { done <- tr.Connect(ctx) }()
	if err := wait(t, done); err == nil || calls.Load() != 0 {
		t.Fatalf("err = %v, hook calls after a refused Register = %d", err, calls.Load())
	}
	go func() { done <- tr.Connect(ctx) }()
	r.server.next(t)
	if calls.Load() != 1 {
		t.Fatalf("hook calls = %d", calls.Load())
	}
}

// Scenario: Отказ CERT_SUPERSEDED при годной новой паре переводит агента на неё
func TestASupersededCertificateWithASpareReconnectsAtOnce(t *testing.T) {
	r := newRig(t)
	r.server.register = func() (*agentv1.RegisterResponse, error) {
		if len(r.server.registered) == 1 {
			return nil, reasoned(codes.Unauthenticated, "superseded", "sard.dev", "CERT_SUPERSEDED", nil)
		}
		return registered("a", beat), nil
	}
	var asked atomic.Int32
	tr := hooked(t, r, nil, func(context.Context) bool { asked.Add(1); return true })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- tr.Run(ctx) }()
	r.server.next(t) // the stream of the second Register
	if asked.Load() != 1 || r.server.registerCount() != 2 {
		t.Fatalf("asked = %d, Register = %d", asked.Load(), r.server.registerCount())
	}
	if len(r.clock.added) > 1 { // only the heartbeat timer, no backoff
		t.Fatalf("timers = %d", len(r.clock.added))
	}
}

// Scenario: Отказ CERT_SUPERSEDED без другой пары завершает агента с командой перерегистрации
func TestASupersededCertificateWithoutASpareIsFinal(t *testing.T) {
	r := newRig(t)
	r.server.register = func() (*agentv1.RegisterResponse, error) {
		return nil, reasoned(codes.Unauthenticated, "superseded", "sard.dev", "CERT_SUPERSEDED", nil)
	}
	tr := hooked(t, r, nil, func(context.Context) bool { return false })
	done := make(chan error, 1)
	go func() { done <- tr.Run(context.Background()) }()
	if err := wait(t, done); !transport.IsPermanent(err) || r.server.registerCount() != 1 {
		t.Fatalf("err = %v, Register = %d", err, r.server.registerCount())
	}
}

func TestOtherRefusalsDoNotAskForASpare(t *testing.T) {
	r := newRig(t)
	r.server.register = func() (*agentv1.RegisterResponse, error) {
		return nil, reasoned(codes.Unauthenticated, "revoked", "sard.dev", "AGENT_REVOKED", nil)
	}
	tr := hooked(t, r, nil, func(context.Context) bool { t.Error("asked"); return true })
	done := make(chan error, 1)
	go func() { done <- tr.Run(context.Background()) }()
	if err := wait(t, done); !transport.IsPermanent(err) {
		t.Fatalf("err = %v", err)
	}
}
