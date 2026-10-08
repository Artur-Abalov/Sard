// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"context"
	"testing"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

type stateHost struct{ sdk.Host }

// The state of a step is removed by Dump whatever Dump finds wrong.
func TestDumpRemovesTheStateOfTheStepEvenOnABadConfig(t *testing.T) {
	h := &stateHost{}
	prepared.Store(h, checked{})
	if _, err := (Plugin{}).Dump(context.Background(), h, sdk.Config("not json")); err == nil {
		t.Fatal("Dump accepted a bad config")
	}
	if _, ok := prepared.Load(h); ok {
		t.Error("the state of the step is still there")
	}
}
