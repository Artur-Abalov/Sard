// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package sandbox_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/sandbox"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

var _ sandbox.Sandbox = sandbox.Stub{}

func TestSandboxStubIsNotImplemented(t *testing.T) {
	c, err := (sandbox.Stub{}).Start(context.Background(), sandbox.Spec{Image: "postgres:18"})
	if c != nil || !errors.Is(err, sdk.ErrNotImplemented) {
		t.Fatalf("Start = %v, %v", c, err)
	}
}
