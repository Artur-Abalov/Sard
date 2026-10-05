// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:build e2e

package plugins

import (
	"github.com/Artur-Abalov/sard/agent/plugins/e2eslow"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// stand returns the e2e stand's plugins; only `make e2e-images` builds
// with the "e2e" tag (T3s).
func stand(agentVersion string) []sdk.Plugin {
	return []sdk.Plugin{e2eslow.Plugin{AgentVersion: agentVersion}}
}
