// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

package sdk_test

import (
	"testing"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// Levels are ordered by severity, and the zero Level is "unset", not a
// level: hosts treat an unset level as their default.
func TestLevelsAreOrderedAndZeroIsNotALevel(t *testing.T) {
	levels := []sdk.Level{sdk.LevelDebug, sdk.LevelInfo, sdk.LevelWarn, sdk.LevelError}
	for i, l := range levels {
		if want := sdk.Level(i + 1); l != want {
			t.Errorf("level #%d = %d, want %d", i, l, want)
		}
	}
}
