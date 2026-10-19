// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package certpair

import (
	"errors"
	"io/fs"
	"path/filepath"
	"testing"
)

func TestSyncDirReportsADirectoryThatIsNotThere(t *testing.T) {
	t.Parallel()
	if err := syncDir(filepath.Join(t.TempDir(), "absent")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want the directory is not there", err)
	}
	if err := syncDir(t.TempDir()); err != nil {
		t.Fatalf("err = %v", err)
	}
}
