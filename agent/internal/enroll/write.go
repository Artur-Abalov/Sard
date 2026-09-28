// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package enroll

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Files are the three paths a successful enrollment writes (В19): the
// private key, the agent certificate and the CA bundle.
type Files struct {
	KeyFile  string
	CertFile string
	CAFile   string
}

// keyMode and bundleMode are fixed regardless of the process umask (rule
// "Файлы принадлежат запустившему пользователю, ключ закрыт").
const (
	keyMode    os.FileMode = 0o600
	bundleMode os.FileMode = 0o644
)

const writeProbePrefix = ".sard-enroll-probe-"
const writeTempPrefix = ".sard-enroll-"

// CheckWritable checks that each of files' directories exists and can be
// written to, without creating anything (В12: directories are never
// created by enroll). The first directory that fails is named in the
// returned ClassWrite *Error.
func CheckWritable(files Files) error {
	for _, f := range []struct{ key, path string }{
		{"tls.key_file", files.KeyFile},
		{"tls.cert_file", files.CertFile},
		{"tls.ca_file", files.CAFile},
	} {
		dir := filepath.Dir(f.path)
		if err := probeWritable(dir); err != nil {
			return &Error{Class: ClassWrite, msg: fmt.Sprintf("%s: directory %s is not writable", f.key, dir), err: err}
		}
	}
	return nil
}

func probeWritable(dir string) error {
	tmp, err := os.CreateTemp(dir, writeProbePrefix+"*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_ = tmp.Close()
	return os.Remove(name)
}

// WriteIdentity atomically replaces the key, certificate and CA bundle:
// either all three land or none do (rule "Неудачная запись не оставляет
// полуготовой идентичности"). Every file is first written to a temporary
// name next to its target, fsynced and closed; only once every write has
// succeeded does WriteIdentity rename them into place, CA first and the
// certificate last (F4): the certificate is what InspectIdentity reads
// agent_id from, so it is the single marker of "a full identity landed" —
// a reader can never see a certificate whose key or CA bundle is missing.
// A failure before the first rename — including a target path that
// already exists as something other than a regular file, checked before
// any temporary is even created — leaves whatever was there before
// completely untouched, temporaries included. A failure between renames
// removes every not-yet-renamed temporary; on a first enrollment (nothing
// existed at any of the three paths before this call) it also undoes the
// renames that did land, since a lone new file without its siblings is
// worse than nothing. Overwriting an existing identity (--force) cannot
// be undone the same way — some of the three renames may already have
// replaced the previous files — so what already landed is left in place;
// this is the one window rule 7 cannot fully close (documented in
// docs/sessions/2026-09-28-a2a-enroll-mechanism.md).
func WriteIdentity(files Files, key, cert, ca []byte) error {
	hadPrevious, err := anyTargetExists(files)
	if err != nil {
		return &Error{Class: ClassWrite, msg: "checking the existing identity failed", err: err}
	}
	staged, err := stageAll(files, key, cert, ca)
	if err != nil {
		removeAll(staged)
		return err
	}
	return commitAll(commitOrder(staged, files), hadPrevious)
}

func commitAll(order []stagedFile, hadPrevious bool) error {
	for i, s := range order {
		if err := commit(s); err != nil {
			return handleCommitFailure(order, i, hadPrevious, err)
		}
	}
	return nil
}

func handleCommitFailure(order []stagedFile, i int, hadPrevious bool, err error) error {
	removeAll(order[i:]) // s's own temp survives a failed rename too
	if !hadPrevious {
		removeCommitted(order[:i])
	}
	return &Error{Class: ClassWrite, msg: "writing the identity failed partway through", err: err}
}

// anyTargetExists reports whether any of the three paths already has
// something at it — the write is then a --force overwrite, and a partial
// failure cannot be safely rolled back by deleting what landed.
func anyTargetExists(files Files) (bool, error) {
	for _, path := range []string{files.KeyFile, files.CertFile, files.CAFile} {
		switch _, err := os.Lstat(path); {
		case err == nil:
			return true, nil
		case os.IsNotExist(err):
			// keep checking the others
		default:
			return false, err
		}
	}
	return false, nil
}

type stagedFile struct {
	tempPath  string
	finalPath string
}

func stageAll(files Files, key, cert, ca []byte) ([]stagedFile, error) {
	specs := []struct {
		path string
		data []byte
		mode os.FileMode
	}{
		{files.KeyFile, key, keyMode},
		{files.CertFile, cert, bundleMode},
		{files.CAFile, ca, bundleMode},
	}
	staged := make([]stagedFile, 0, len(specs))
	for _, s := range specs {
		if err := checkTargetReplaceable(s.path); err != nil {
			return staged, &Error{Class: ClassWrite, msg: s.path + " exists and is not a regular file", err: err}
		}
		tmp, err := stage(s.path, s.data, s.mode)
		if err != nil {
			return staged, &Error{Class: ClassWrite, msg: "writing " + s.path + " failed", err: err}
		}
		staged = append(staged, stagedFile{tempPath: tmp, finalPath: s.path})
	}
	return staged, nil
}

// checkTargetReplaceable rejects a target that exists as something other
// than a regular file (a directory, most often) before any temporary file
// is created: renaming onto it would fail anyway, but only after leaving
// earlier renames committed and this file's temporary behind (F4). Any
// other Lstat outcome — the path does not exist, or Lstat itself fails
// (e.g. a parent that is not a directory) — is left to stage()'s own
// error handling, unchanged from before this check existed.
func checkTargetReplaceable(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return nil
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file (mode %v)", path, info.Mode())
	}
	return nil
}

// commitOrder is CA, key, cert (F4): the certificate is committed last, so
// it alone marks a complete identity.
func commitOrder(staged []stagedFile, files Files) []stagedFile {
	order := make([]stagedFile, 0, len(staged))
	for _, final := range []string{files.CAFile, files.KeyFile, files.CertFile} {
		for _, s := range staged {
			if s.finalPath == final {
				order = append(order, s)
			}
		}
	}
	return order
}

// removeCommitted deletes files WriteIdentity already renamed into place,
// undoing them because there was nothing at any of the three paths before
// this call — an incomplete new identity is worse than none.
func removeCommitted(committed []stagedFile) {
	for _, s := range committed {
		_ = os.Remove(s.finalPath)
	}
}

func stage(path string, data []byte, mode os.FileMode) (string, error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, writeTempPrefix+"*")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	err = errors.Join(werr, tmp.Chmod(mode), tmp.Sync(), tmp.Close())
	if err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

// renameFile is os.Rename, indirected so tests can fail a specific commit
// deterministically (export_test.go) instead of racing the filesystem to
// fail a rename after staging already succeeded.
var renameFile = os.Rename

func commit(s stagedFile) error {
	if err := renameFile(s.tempPath, s.finalPath); err != nil {
		return err
	}
	return syncParent(s.finalPath)
}

func syncParent(path string) error {
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}

func removeAll(staged []stagedFile) {
	for _, s := range staged {
		_ = os.Remove(s.tempPath)
	}
}
