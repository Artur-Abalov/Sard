// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
)

// A8c-2 (Р59): a held directory tells what it holds and is locked for one
// restore at a time.
func TestNamesAreTheEntriesOfTheHeldDirectory(t *testing.T) {
	base := t.TempDir()
	ok(t, os.WriteFile(filepath.Join(base, "a"), nil, 0o600))
	ok(t, os.Mkdir(filepath.Join(base, "b"), 0o700))
	names, err := heldDir(t, base).Names()
	slices.Sort(names)
	if err != nil || !slices.Equal(names, []string{"a", "b"}) {
		t.Fatalf("names %v, err %v", names, err)
	}
}

func TestAHeldDirectoryIsLockedForOneHolderUntilItIsClosed(t *testing.T) {
	base := t.TempDir()
	first := heldDir(t, base)
	ok(t, first.Lock())
	second := heldDir(t, base)
	if err := second.Lock(); !errors.Is(err, hostsetup.ErrBusy) {
		t.Fatalf("second lock: %v", err)
	}
	ok(t, first.Close())
	ok(t, second.Lock())
}
