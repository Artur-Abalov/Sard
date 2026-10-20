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
	"strings"
)

// Dir is an open directory. Names given to Open and Mkdir are one
// component, resolved relative to this very directory (openat), never
// through a symbolic link: a link in the way is an error, not a detour.
type Dir interface {
	// Path is the path this Dir was reached by, for messages.
	Path() string
	// Open opens the directory called name; fs.ErrNotExist if there is
	// none, *NotDirError if it is a link or not a directory.
	Open(name string) (Dir, error)
	// Mkdir makes the directory called name and opens it.
	Mkdir(name string, perm fs.FileMode) (Dir, error)
	// Chown and Chmod change this directory itself (fchown, fchmod).
	Chown(uid, gid int) error
	Chmod(mode fs.FileMode) error
	Stat() (fs.FileInfo, error)
	Close() error

	// CreateFile makes the regular file called name and opens it for
	// writing: openat with O_CREAT|O_EXCL|O_NOFOLLOW, so whatever has the
	// name already, a link included, is an fs.ErrExist and is not touched.
	CreateFile(name string, perm fs.FileMode) (File, error)
	// OpenFile opens the file called name for reading without following a
	// link (syscall.ELOOP) and without blocking on a FIFO; the caller
	// judges what it is from Stat of the descriptor.
	OpenFile(name string) (ReadFile, error)
	// Lstat describes what name is, a link as a link, without opening it
	// for reading (an O_PATH descriptor): for what root must not read.
	Lstat(name string) (fs.FileInfo, error)
	// Names are the names of the entries of this directory, in no order.
	Names() ([]string, error)
	// Lock takes an exclusive flock on this directory's descriptor, without
	// waiting: ErrBusy if another holder has it. It is released by Close.
	Lock() error
	// Rename, Remove and Sync are renameat, unlinkat and fsync relative to
	// this directory; names are one component.
	Rename(oldName, newName string) error
	Remove(name string) error
	Sync() error

	// Handle is the open descriptor of this directory, for a process that
	// inherits it (restic gets the staging directory of a restore this way).
	// It is owned by the Dir: it is closed with it.
	Handle() *os.File
	// RenameNoReplace moves the entry oldName of this directory to newName in
	// newDir (renameat2 with RENAME_NOREPLACE): a name that exists there is
	// fs.ErrExist and is not touched; another file system is ErrCrossDevice.
	RenameNoReplace(oldName string, newDir Dir, newName string) error
	// RemoveDir removes the empty directory called name (unlinkat with
	// AT_REMOVEDIR).
	RemoveDir(name string) error
	// Move renames the entry oldName of this directory to newName in newDir
	// (renameat): the name appears with the whole entry or not at all. A name
	// that is taken by an empty directory is replaced by it; one taken by
	// anything else is not (ENOTEMPTY, ENOTDIR). Another file system is
	// ErrCrossDevice.
	Move(oldName string, newDir Dir, newName string) error
}

// ErrCrossDevice: the entry cannot be moved because the two directories are
// on different file systems, a mount point lies between them (EXDEV).
var ErrCrossDevice = errors.New("the directories are on different file systems")

// ErrBusy: the directory is locked by another holder.
var ErrBusy = errors.New("the directory is held by another command")

// ReadFile is a file opened for reading.
type ReadFile interface {
	io.Reader
	Stat() (fs.FileInfo, error)
	Close() error
}

// NotDirError is a component of a path that is not a directory: most
// often a symbolic link, which the walk refuses to follow.
type NotDirError struct {
	Path    string
	Symlink bool
}

func (e *NotDirError) Error() string {
	if e.Symlink {
		return e.Path + " is a symbolic link; give the resolved path"
	}
	return e.Path + " is not a directory"
}

// Make says how OpenDir creates the components that are missing: the
// parents and the last one may differ in owner and mode.
type Make struct{ Parents, Last Attrs }

// OpenDir opens the directory at the absolute path by walking it from "/"
// one component at a time, each opened relative to the directory already
// held and without following a link; that holds the walk to the
// directories it names even if the service user swaps one of them for a
// link meanwhile. With a Make the missing components are created the same
// way (mkdirat on the held parent) and given their owner and mode; the
// paths created are returned. Without one a missing component is
// fs.ErrNotExist.
func OpenDir(fsys FS, path string, mk *Make) (Dir, []string, error) {
	if !filepath.IsAbs(path) {
		return nil, nil, fmt.Errorf("%s is not an absolute path", path)
	}
	cur, err := fsys.OpenRootDir()
	if err != nil {
		return nil, nil, err
	}
	return walk(cur, components(path), mk)
}

// walk opens the names one after the other from cur, which it closes along
// with every directory it passes; it returns the paths it had to create.
func walk(cur Dir, names []string, mk *Make) (Dir, []string, error) {
	var created []string
	for i, name := range names {
		next, made, err := step(cur, name, mk, i == len(names)-1)
		_ = cur.Close()
		if made {
			created = append(created, next.Path())
		}
		if err != nil {
			return nil, created, err
		}
		cur = next
	}
	return cur, created, nil
}

// components are the names of a clean absolute path; none for "/".
func components(path string) []string {
	return strings.FieldsFunc(filepath.Clean(path), func(r rune) bool { return r == '/' })
}

// step opens one component of a walk, making it if allowed and missing.
func step(cur Dir, name string, mk *Make, last bool) (next Dir, made bool, err error) {
	next, err = cur.Open(name)
	if !errors.Is(err, fs.ErrNotExist) || mk == nil {
		return next, false, err
	}
	attrs := mk.Parents
	if last {
		attrs = mk.Last
	}
	next, err = makeComponent(cur, name, attrs)
	return next, err == nil, err
}

// makeComponent makes the directory name in cur with its owner and mode.
func makeComponent(cur Dir, name string, a Attrs) (Dir, error) {
	d, err := cur.Mkdir(name, a.Mode)
	if err != nil {
		return nil, &WriteError{Path: cur.Path() + "/" + name, Op: "create directory", Err: err}
	}
	if err := errors.Join(d.Chown(a.UID, a.GID), d.Chmod(a.Mode)); err != nil {
		_ = d.Close()
		return nil, &WriteError{Path: d.Path(), Op: "create directory", Err: err}
	}
	return d, nil
}
