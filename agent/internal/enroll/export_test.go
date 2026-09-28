// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package enroll

// Internals exposed to the external tests of this package.

// SetRenameForTest replaces the rename step WriteIdentity's commit uses
// with fn, and returns a function that restores the real os.Rename.
// Needed to fail one specific commit deterministically (which file, which
// attempt) — racing the real filesystem to fail a rename after staging
// has already succeeded is not reproducible.
func SetRenameForTest(fn func(oldpath, newpath string) error) (restore func()) {
	old := renameFile
	renameFile = fn
	return func() { renameFile = old }
}
