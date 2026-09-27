// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package session_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/session"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

var _ session.Session = session.Stub{}

func TestSessionStubIsNotImplemented(t *testing.T) {
	if err := (session.Stub{}).Serve(context.Background(), nil); !errors.Is(err, sdk.ErrNotImplemented) {
		t.Fatalf("err = %v", err)
	}
}
