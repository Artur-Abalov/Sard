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

// modeFile is a freshly created file whose Chmod and Close can fail.
type modeFile struct {
	*os.File
	chmodErr, closeErr error
}

func (m modeFile) Chmod(mode os.FileMode) error {
	if m.chmodErr != nil {
		return m.chmodErr
	}
	return m.File.Chmod(mode)
}

func (m modeFile) Close() error {
	err := m.File.Close()
	if m.closeErr != nil {
		return m.closeErr
	}
	return err
}

func openWith(mode os.FileMode, chmodErr, closeErr error) func(string) (passwordFile, error) {
	return func(name string) (passwordFile, error) {
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		return modeFile{f, chmodErr, closeErr}, err
	}
}

func TestAPasswordFileIsOwnerOnlyWhateverModeItWasCreatedWith(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pass")
	if err := writeNew(openWith(0o200, nil, nil), path, []byte("secret")); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("%v, %v", info, err)
	}
}

func TestAPasswordFileThatCannotBeSecuredOrClosedIsNotKept(t *testing.T) {
	for name, open := range map[string]func(string) (passwordFile, error){
		"chmod": openWith(0o600, errors.New("operation not permitted"), nil),
		"close": openWith(0o600, nil, errors.New("disk error")),
	} {
		path := filepath.Join(t.TempDir(), "pass")
		if err := writeNew(open, path, []byte("secret")); err == nil {
			t.Errorf("%s: the error was lost", name)
		}
		if _, err := os.Stat(path); err == nil {
			t.Errorf("%s: the file is left behind", name)
		}
	}
}
