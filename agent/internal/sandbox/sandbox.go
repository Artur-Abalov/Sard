// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package sandbox starts temporary Docker/Podman containers in which a
// restored copy is brought up and checked.
package sandbox

import (
	"context"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// Spec describes a throwaway container.
type Spec struct {
	Image string
	Env   []string
	// Mounts maps host paths to container paths.
	Mounts map[string]string
}

// Container is a running throwaway container.
type Container interface {
	// Exec runs a command inside the container and returns its output.
	Exec(ctx context.Context, cmd []string) ([]byte, error)
	// Stop removes the container.
	Stop(ctx context.Context) error
}

// Sandbox starts containers.
type Sandbox interface {
	Start(ctx context.Context, spec Spec) (Container, error)
}

// Stub is the placeholder Sandbox.
type Stub struct{}

// Start will talk to the Docker/Podman API (roadmap: restore verification, stage 2).
func (Stub) Start(context.Context, Spec) (Container, error) {
	return nil, sdk.ErrNotImplemented
}
