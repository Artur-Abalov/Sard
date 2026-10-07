// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostsetup

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// File is a file being written: what os.File offers, so a test can make
// any step fail.
type File interface {
	io.Writer
	Name() string
	Chown(uid, gid int) error
	Chmod(mode os.FileMode) error
	Sync() error
	Close() error
}

// FS is the part of the file system the commands change. OS is the real
// one; tests wrap it to record owner changes (a test is not root) and to
// fail a step on demand.
type FS interface {
	CreateTemp(dir, pattern string) (File, error)
	Rename(oldpath, newpath string) error
	Remove(path string) error
	// SyncDir makes the entries of dir durable.
	SyncDir(dir string) error
	Mkdir(path string, perm os.FileMode) error
	// Chown is lchown: a symbolic link itself changes owner.
	Chown(path string, uid, gid int) error
	Chmod(path string, mode os.FileMode) error
	Stat(path string) (fs.FileInfo, error)
	ReadFile(path string) ([]byte, error)
	ReadDir(path string) ([]fs.DirEntry, error)
}

// OS is FS on the host's own file system.
type OS struct{}

// CreateTemp implements FS.
func (OS) CreateTemp(dir, pattern string) (File, error) { return os.CreateTemp(dir, pattern) }

// Rename implements FS.
func (OS) Rename(oldpath, newpath string) error { return os.Rename(oldpath, newpath) }

// Remove implements FS.
func (OS) Remove(path string) error { return os.Remove(path) }

// Mkdir implements FS.
func (OS) Mkdir(path string, perm os.FileMode) error { return os.Mkdir(path, perm) }

// Chown implements FS.
func (OS) Chown(path string, uid, gid int) error { return os.Lchown(path, uid, gid) }

// Chmod implements FS.
func (OS) Chmod(path string, mode os.FileMode) error { return os.Chmod(path, mode) }

// Stat implements FS.
func (OS) Stat(path string) (fs.FileInfo, error) { return os.Stat(path) }

// ReadFile implements FS.
func (OS) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

// ReadDir implements FS.
func (OS) ReadDir(path string) ([]fs.DirEntry, error) { return os.ReadDir(path) }

// SyncDir implements FS.
func (OS) SyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}

// WriteError is a write that failed; every message names Path, the file
// the operator knows, never a temporary one.
type WriteError struct {
	Path string
	Op   string
	Err  error
}

func (e *WriteError) Error() string { return fmt.Sprintf("%s %s: %v", e.Op, e.Path, e.Err) }

// Unwrap lets errors.Is see the cause.
func (e *WriteError) Unwrap() error { return e.Err }

// Attrs are the owner and mode of what a command creates; they do not
// depend on the umask.
type Attrs struct {
	UID, GID int
	Mode     os.FileMode
}

// WriteFile writes data to path atomically (Р4): into a temporary file in
// the same directory that gets its owner and mode first, then fsync, an
// atomic rename and fsync of the directory. A failure leaves the previous
// content and no temporary file (a failed directory sync comes after the
// rename: the new content is in place).
func WriteFile(fsys FS, path string, data []byte, a Attrs) error {
	dir := filepath.Dir(path)
	tmp, err := fsys.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return &WriteError{Path: path, Op: "create", Err: err}
	}
	if err := fill(tmp, data, a); err != nil {
		_ = tmp.Close()
		_ = fsys.Remove(tmp.Name())
		return &WriteError{Path: path, Op: "write", Err: err}
	}
	if err := fsys.Rename(tmp.Name(), path); err != nil {
		_ = fsys.Remove(tmp.Name())
		return &WriteError{Path: path, Op: "rename", Err: err}
	}
	if err := fsys.SyncDir(dir); err != nil {
		return &WriteError{Path: path, Op: "sync", Err: err}
	}
	return nil
}

// fill gives the temporary file its owner and mode, then its content.
func fill(f File, data []byte, a Attrs) error {
	if err := f.Chown(a.UID, a.GID); err != nil {
		return err
	}
	if err := f.Chmod(a.Mode); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	return f.Close()
}

// EnsureDir creates the directory, one level, with the owner and mode
// given (the mode set again: the umask must not take bits from it). An
// existing directory is left as it is. It reports whether it was created.
func EnsureDir(fsys FS, path string, a Attrs) (bool, error) {
	info, err := fsys.Stat(path)
	switch {
	case err == nil && info.IsDir():
		return false, nil
	case err == nil:
		return false, &WriteError{Path: path, Op: "create directory", Err: errors.New("exists and is not a directory")}
	case !errors.Is(err, fs.ErrNotExist):
		return false, &WriteError{Path: path, Op: "create directory", Err: err}
	}
	return true, makeDir(fsys, path, a)
}

func makeDir(fsys FS, path string, a Attrs) error {
	for _, step := range []func() error{
		func() error { return fsys.Mkdir(path, a.Mode) },
		func() error { return fsys.Chown(path, a.UID, a.GID) },
		func() error { return fsys.Chmod(path, a.Mode) },
	} {
		if err := step(); err != nil {
			return &WriteError{Path: path, Op: "create directory", Err: err}
		}
	}
	return nil
}

// RemoveFile removes the file; a missing one is not an error. It reports
// whether there was one.
func RemoveFile(fsys FS, path string) (bool, error) {
	err := fsys.Remove(path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	}
	return false, &WriteError{Path: path, Op: "remove", Err: err}
}

// ChownTree gives root and everything below it, symbolic links as
// themselves, to the owner; modes are not touched (Н7).
func ChownTree(fsys FS, root string, uid, gid int) error {
	if err := fsys.Chown(root, uid, gid); err != nil {
		return &WriteError{Path: root, Op: "change owner of", Err: err}
	}
	entries, err := fsys.ReadDir(root)
	if err != nil {
		return &WriteError{Path: root, Op: "read directory", Err: err}
	}
	for _, e := range entries {
		path := filepath.Join(root, e.Name())
		if e.IsDir() {
			err = ChownTree(fsys, path, uid, gid)
		} else if cerr := fsys.Chown(path, uid, gid); cerr != nil {
			err = &WriteError{Path: path, Op: "change owner of", Err: cerr}
		}
		if err != nil {
			return err
		}
	}
	return nil
}
