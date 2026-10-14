// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
)

func TestSameFileTellsOneFileFromAnotherWithTheSameContent(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(dir, "link")
	if err := os.Link(a, link); err != nil {
		t.Fatal(err)
	}
	info := func(p string) os.FileInfo {
		i, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		return i
	}
	if !hostsetup.SameFile(info(a), info(link)) || hostsetup.SameFile(info(a), info(b)) {
		t.Error("same file wrong")
	}
}
