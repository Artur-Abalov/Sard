// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package certpair

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/Artur-Abalov/sard/agent/internal/config"
)

// File modes are fixed whatever the umask (П16).
const (
	keyMode  os.FileMode = 0o600
	certMode os.FileMode = 0o644
)

// NewPair is where a renewed pair is written: next to the current one.
func NewPair(files config.TLS) Pair {
	return Pair{KeyFile: files.KeyFile + NewSuffix, CertFile: files.CertFile + NewSuffix}
}

// WriteNew writes the renewed key and then its certificate next to the
// current pair. When either write fails nothing of the new pair is left.
func WriteNew(files config.TLS, fsys FS, keyPEM, certPEM []byte) error {
	next := NewPair(files)
	if err := fsys.WriteFile(next.KeyFile, keyPEM, keyMode); err != nil {
		return err
	}
	if err := fsys.WriteFile(next.CertFile, certPEM, certMode); err != nil {
		_ = fsys.Remove(next.KeyFile)
		return err
	}
	return nil
}

// Promote renames the ".new" files of sel over the current ones, the key
// first (П18). Between the two renames the key belongs to the new
// certificate and the current certificate is still in place: Select
// recognizes that pair.
func Promote(fsys FS, files config.TLS, sel Selection) error {
	var errs []error
	if sel.KeyIsNew {
		errs = append(errs, fsys.Rename(sel.Pair.KeyFile, files.KeyFile))
	}
	if sel.CertIsNew && errors.Join(errs...) == nil {
		errs = append(errs, fsys.Rename(sel.Pair.CertFile, files.CertFile))
	}
	return errors.Join(errs...)
}

// Discard removes the renewal files of the pair and returns what it removed.
func Discard(fsys FS, files config.TLS) []string {
	var found []string
	for _, dir := range dirsOf(files) {
		entries, err := fsys.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			path := filepath.Join(dir, e.Name())
			if isRenewalFile(files, path) {
				found = append(found, path)
			}
		}
	}
	return Remove(fsys, found)
}

// Remove deletes the files and returns those that were deleted.
func Remove(fsys FS, paths []string) []string {
	var removed []string
	for _, p := range paths {
		if fsys.Remove(p) == nil {
			removed = append(removed, p)
		}
	}
	return removed
}
