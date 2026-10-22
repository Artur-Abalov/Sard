// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"crypto/rand"
	"io"
	"log/slog"
	mathrand "math/rand/v2"

	"github.com/Artur-Abalov/sard/agent/internal/certpair"
	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/transport"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// agentRuntime is what the running agent takes from the outside world: time,
// randomness, the log and the file system of its certificate. Its zero value
// is the production one (withDefaults); tests move the clock by hand.
type agentRuntime struct {
	clock  transport.Clock
	rand   func() float64
	random io.Reader
	logger *slog.Logger
	fs     certpair.FS
	// handlers, when set, run the steps instead of the built-in plugins (tests).
	handlers executor.Registry
	// journalPlugins, when set, are the plugins whose restore journal entries
	// the start recovers, instead of the built-in ones (tests).
	journalPlugins *sdk.Registry
}

func (r agentRuntime) withDefaults() agentRuntime {
	if r.clock == nil {
		r.clock = realEnrollClock{}
	}
	if r.rand == nil {
		r.rand = mathrand.Float64
	}
	if r.random == nil {
		r.random = rand.Reader
	}
	if r.logger == nil {
		r.logger = slog.Default()
	}
	if r.fs == nil {
		r.fs = certpair.OSFS{}
	}
	return r
}
