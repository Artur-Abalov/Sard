// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package certpair

import (
	"errors"
	"os"
	"path/filepath"
)

// FS is the file system the pair lives on; tests fail its calls one by one.
type FS interface {
	ReadFile(name string) ([]byte, error)
	// ReadDir lists a directory; names only are used.
	ReadDir(dir string) ([]os.DirEntry, error)
	Remove(name string) error
	// Rename moves a file over another and makes the move durable (fsync of
	// the directory).
	Rename(oldPath, newPath string) error
	// WriteFile creates path with exactly this mode, whatever the umask: a
	// temporary file next to it is written, fsynced and renamed, and the
	// directory is fsynced. A failure leaves neither path nor a temporary.
	WriteFile(path string, data []byte, mode os.FileMode) error
}

// TempPrefix starts the name of the temporary files of a renewal.
const TempPrefix = ".sard-renew-"

// OSFS is the host's file system.
type OSFS struct{}

// ReadFile is os.ReadFile.
func (OSFS) ReadFile(name string) ([]byte, error) { return os.ReadFile(name) }

// ReadDir is os.ReadDir.
func (OSFS) ReadDir(dir string) ([]os.DirEntry, error) { return os.ReadDir(dir) }

// Remove is os.Remove.
func (OSFS) Remove(name string) error { return os.Remove(name) }

// Rename is os.Rename followed by an fsync of the directory.
func (OSFS) Rename(oldPath, newPath string) error {
	if err := os.Rename(oldPath, newPath); err != nil {
		return err
	}
	return syncDir(filepath.Dir(newPath))
}

// WriteFile writes data as the type comment of FS says; the file belongs to
// the user of the process, which is the service user (П16).
func (OSFS) WriteFile(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), TempPrefix+"*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	if err := errors.Join(werr, tmp.Chmod(mode), tmp.Sync(), tmp.Close()); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return syncDir(filepath.Dir(path))
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}
