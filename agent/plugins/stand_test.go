// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:build !e2e

package plugins_test

import (
	"slices"
	"testing"

	"github.com/Artur-Abalov/sard/agent/plugins"
)

// A release build (no "e2e" tag, as make package builds) registers the four
// source plugins and nothing of the e2e stand: Register reports exactly these.
func TestReleaseBuildRegistersTheFourSourcePluginsOnly(t *testing.T) {
	if got := plugins.Registry("1.2.3").Names(); !slices.Equal(got, []string{"files", "mysql", "network", "postgresql"}) {
		t.Fatalf("Names() = %v", got)
	}
}
