// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup

import (
	"errors"
	"io/fs"
	"syscall"
)

// RemoveTree removes the entry name of dir and everything below it, by
// descriptors: each directory is opened relative to its parent without
// following a link, and a link or a file is unlinked, never opened. What a
// link points at is not touched. An entry that is gone already is fine.
func RemoveTree(dir Dir, name string) error {
	child, err := dir.Open(name)
	var notDir *NotDirError
	switch {
	case errors.As(err, &notDir):
		return dir.Remove(name)
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	}
	defer func() { _ = child.Close() }()
	if err := removeContents(child); err != nil {
		return err
	}
	return dir.RemoveDir(name)
}

// removeContents removes every entry of the directory.
func removeContents(dir Dir) error {
	names, err := dir.Names()
	if err != nil {
		return err
	}
	for _, n := range names {
		if err := RemoveTree(dir, n); err != nil {
			return err
		}
	}
	return nil
}

// OwnerOf is the user and group id of a file as the file system reports it;
// found is false when the file info carries none.
func OwnerOf(info fs.FileInfo) (uid, gid uint32, found bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return st.Uid, st.Gid, true
}
