// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoinit

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// failingFile fails the write, as a full disk does.
type failingFile struct{ *os.File }

func (failingFile) Write([]byte) (int, error) { return 0, errors.New("no space left on device") }

// A password file that could not be written completely must not stay: the
// next run would take the half of a password for the whole.
func TestAFailedWriteLeavesNoPasswordFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pass")
	open := func(name string) (passwordFile, error) {
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		return failingFile{f}, err
	}
	if err := writeNew(open, path, []byte("secret")); err == nil {
		t.Fatal("the write error was lost")
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("a broken password file is left behind")
	}
}
