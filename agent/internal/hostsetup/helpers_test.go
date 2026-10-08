// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup_test

import (
	"fmt"
	"os"
	"path/filepath"
)

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

func writeAll(t interface {
	Helper()
	Fatal(args ...any)
}, files map[string][]byte) {
	t.Helper()
	for path, data := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// ok fails the test on err.
func ok(t interface {
	Helper()
	Fatal(args ...any)
}, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
