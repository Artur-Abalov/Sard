// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package postgresql

import (
	"context"
	"errors"
	"testing"
)

func TestAnEndedStepOutranksTheEndOfItsSession(t *testing.T) {
	ended := errors.New("the psql session ended")
	if err := outranked(context.Background(), ended); err != ended {
		t.Errorf("a live step: %v", err)
	}
	cause := errors.New("cancelled by the server")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cause)
	if err := outranked(ctx, ended); err != cause {
		t.Errorf("an ended step: %v", err)
	}
}
