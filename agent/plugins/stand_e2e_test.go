// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:build e2e

package plugins_test

import (
	"slices"
	"testing"

	"github.com/Artur-Abalov/sard/agent/plugins"
)

// The e2e stand's build ("e2e" tag) adds e2e-slow to the source plugins.
func TestStandBuildAddsTheSlowPlugin(t *testing.T) {
	if got := plugins.Registry("1.2.3").Names(); !slices.Equal(got, []string{"e2e-slow", "files", "mysql", "network", "postgresql"}) {
		t.Fatalf("Names() = %v", got)
	}
}
