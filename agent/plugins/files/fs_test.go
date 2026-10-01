// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package files_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/plugins/files"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// Without an FS the plugin looks at the host's own file system.
func TestThePluginChecksTheHostFileSystemByDefault(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	link := filepath.Join(dir, "link")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	prepare := func(p ...string) error {
		return files.Plugin{}.Prepare(context.Background(), nil, []byte(paths(p...)))
	}
	if err := prepare(dir); err != nil {
		t.Errorf("a readable directory: %v", err)
	}
	if err := prepare(file); err != nil {
		t.Errorf("a readable file: %v", err)
	}
	failsWith(t, prepare(filepath.Join(dir, "missing")), "no such file or directory")
	failsWith(t, prepare(link), `symbolic link to "`+file+`"`)
}

func failsWith(t *testing.T, err error, text string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), text) {
		t.Errorf("err = %v, want %q", err, text)
	}
}

// The plugin dumps paths and never streams.
func TestTheFilesPluginDoesNotStream(t *testing.T) {
	if err := (files.Plugin{}).Stream(context.Background(), nil, nil, sdk.Dump{}, io.Discard); !errors.Is(err, sdk.ErrNotImplemented) {
		t.Errorf("Stream: %v", err)
	}
}
