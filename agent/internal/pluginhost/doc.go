// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package pluginhost runs sdk.Plugin sources inside the agent: it
// validates their configs, hands them secrets by name, maps their steps to
// the phases the server shows and connects their dumps to restic, by paths
// or as a stream.
package pluginhost
