// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package runner_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/runner"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

var _ runner.Runner = runner.Stub{}

func TestRunnerStubIsNotImplemented(t *testing.T) {
	if _, err := (runner.Stub{}).Run(context.Background(), runner.Step{Plugin: "files"}); !errors.Is(err, sdk.ErrNotImplemented) {
		t.Fatalf("err = %v", err)
	}
}
