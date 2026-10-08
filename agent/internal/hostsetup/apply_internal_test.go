// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

type unreadableJournalFS struct {
	OS
	err error
}

func (f unreadableJournalFS) ReadDir(string) ([]fs.DirEntry, error) { return nil, f.err }

func TestCountStepsOfAMissingJournalIsNoneAndOfAnUnreadableOneIsNoneWithTheError(t *testing.T) {
	n, err := countSteps(OS{}, filepath.Join(t.TempDir(), "absent"))
	if n != 0 || err != nil {
		t.Fatalf("missing journal: %d, %v", n, err)
	}
	boom := errors.New("permission denied")
	n, err = countSteps(unreadableJournalFS{err: boom}, "/journal")
	if n != 0 || !errors.Is(err, boom) {
		t.Fatalf("unreadable journal: %d, %v", n, err)
	}
}

func TestCountStepsCountsAcceptedCommandsOnly(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"1.json", "2.json", ".3.json", "4.tmp"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "5.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if n, err := countSteps(OS{}, dir); n != 2 || err != nil {
		t.Fatalf("%d, %v", n, err)
	}
}
