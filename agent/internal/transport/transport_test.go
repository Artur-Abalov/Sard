// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package transport_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/transport"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

var (
	_ transport.Client   = (*transport.GRPC)(nil)
	_ transport.Enroller = (*transport.GRPC)(nil)
)

func TestGRPCDialsTheConfiguredAddress(t *testing.T) {
	if got := transport.NewGRPC("sard.example.com:9090").Address(); got != "sard.example.com:9090" {
		t.Fatalf("Address() = %q", got)
	}
}

// Every RPC is a stub until enrollment and mTLS exist; each must return
// a nil response and sdk.ErrNotImplemented.
func TestGRPCStubsAreNotImplemented(t *testing.T) {
	c := transport.NewGRPC("sard.example.com:9090")
	ctx := context.Background()
	calls := map[string]func() (bool, error){
		"Enroll": func() (bool, error) {
			r, err := c.Enroll(ctx, &agentv1.EnrollRequest{})
			return r == nil, err
		},
		"Register": func() (bool, error) {
			r, err := c.Register(ctx, &agentv1.RegisterRequest{})
			return r == nil, err
		},
		"Connect": func() (bool, error) {
			s, err := c.Connect(ctx)
			return s == nil, err
		},
		"RenewCertificate": func() (bool, error) {
			r, err := c.RenewCertificate(ctx, &agentv1.RenewCertificateRequest{})
			return r == nil, err
		},
	}
	for name, call := range calls {
		if isNil, err := call(); !isNil || !errors.Is(err, sdk.ErrNotImplemented) {
			t.Errorf("%s: nil response = %v, err = %v", name, isNil, err)
		}
	}
}
