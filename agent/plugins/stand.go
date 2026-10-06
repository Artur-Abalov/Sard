// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:build !e2e

package plugins

import "github.com/Artur-Abalov/sard/agent/plugins/sdk"

// stand returns the e2e stand's plugins: a release build has none.
func stand(string) []sdk.Plugin { return nil }
