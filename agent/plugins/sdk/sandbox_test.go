// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

package sdk_test

import (
	"errors"
	"testing"

	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

func TestASandboxRefusalSaysWhy(t *testing.T) {
	var err error = &sdk.SandboxRefusal{Reason: "the image has no extension postgis"}
	var refusal *sdk.SandboxRefusal
	if !errors.As(err, &refusal) || err.Error() != "the image has no extension postgis" {
		t.Fatalf("err = %v", err)
	}
}
