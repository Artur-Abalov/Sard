// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package verifier

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// stNoExec is ST_NOEXEC of statvfs(3): the file system executes nothing.
const stNoExec = 0x8

// OSDisk is the work volume on the file system of the host.
type OSDisk struct {
	// Statfs is syscall.Statfs; tests replace it.
	Statfs func(path string, st *syscall.Statfs_t) error
}

var _ Disk = OSDisk{}

// Volume implements Disk: the bytes the service user may write and the noexec flag.
func (d OSDisk) Volume(dir string) (Volume, error) {
	statfs := d.Statfs
	if statfs == nil {
		statfs = syscall.Statfs
	}
	var st syscall.Statfs_t
	if err := statfs(dir, &st); err != nil {
		return Volume{}, err
	}
	return Volume{Free: st.Bavail * uint64(st.Bsize), NoExec: st.Flags&stNoExec != 0}, nil
}

// MakeDir implements Disk.
func (OSDisk) MakeDir(path string) error { return os.Mkdir(path, 0o700) }

// RemoveAll implements Disk. A copy keeps the modes of the snapshot: a
// directory it cannot write into is made writable, and the removal tried again.
func (OSDisk) RemoveAll(path string) error {
	if os.RemoveAll(path) == nil {
		return nil
	}
	_ = filepath.WalkDir(path, func(p string, e fs.DirEntry, err error) error {
		if err == nil && e.IsDir() {
			_ = os.Chmod(p, 0o700)
		}
		return nil
	})
	return os.RemoveAll(path)
}

// Entries implements Disk.
func (OSDisk) Entries(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	return names, nil
}
