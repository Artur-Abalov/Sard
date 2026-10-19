// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

package pgclient_test

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// There is one implementation of the environment of a PostgreSQL client.
func TestOnlyPgclientPutsThePasswordIntoTheEnvironment(t *testing.T) {
	root := "../../.."
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		data, _ := os.ReadFile(p)
		if bytes.Contains(data, []byte(`"PGPASSWORD="`)) && !strings.Contains(filepath.ToSlash(p), "plugins/sdk/pgclient/") {
			t.Errorf("%s sets PGPASSWORD itself; use pgclient.Env", p)
		}
		return nil
	})
}
