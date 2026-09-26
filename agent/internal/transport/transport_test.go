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

var _ transport.Client = (*transport.GRPC)(nil)

func TestGRPCStubIsNotImplemented(t *testing.T) {
	c := transport.NewGRPC("sard.example.com:9090")
	if c.Address() != "sard.example.com:9090" {
		t.Errorf("Address() = %q", c.Address())
	}
	if resp, err := c.Register(context.Background(), &agentv1.RegisterRequest{}); resp != nil || !errors.Is(err, sdk.ErrNotImplemented) {
		t.Errorf("Register = %v, %v", resp, err)
	}
	if s, err := c.Connect(context.Background()); s != nil || !errors.Is(err, sdk.ErrNotImplemented) {
		t.Errorf("Connect = %v, %v", s, err)
	}
}
