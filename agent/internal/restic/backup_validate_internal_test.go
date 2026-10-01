// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic

import (
	"context"
	"errors"
	"io"
	"testing"
)

// A6b: --one-file-system belongs to a dump by paths; with a stream it is a
// contract violation of the plugin, not something to ignore silently.
func TestStdinWithOneFileSystemIsAnInvalidRequest(t *testing.T) {
	req := BackupRequest{
		Stdin:         func(context.Context, io.Writer) error { return nil },
		StdinFilename: "db.sql",
		OneFileSystem: true,
	}
	if err := req.validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("validate = %v, want ErrInvalidRequest", err)
	}
}
