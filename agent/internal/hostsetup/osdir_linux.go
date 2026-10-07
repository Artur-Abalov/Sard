// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:build linux

package hostsetup

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// osDir is a directory held by its file descriptor; every step of a walk
// is openat/mkdirat relative to it (stdlib syscall, no dependency).
type osDir struct{ f *os.File }

// OpenRootDir implements FS.
func (OS) OpenRootDir() (Dir, error) {
	f, err := os.Open("/")
	if err != nil {
		return nil, err
	}
	return osDir{f}, nil
}

func (d osDir) Path() string { return d.f.Name() }

func (d osDir) child(name string) string { return filepath.Join(d.f.Name(), name) }

// Open implements Dir: O_NOFOLLOW|O_DIRECTORY, so a link or a file at the
// end is an error, never a detour.
func (d osDir) Open(name string) (Dir, error) {
	fd, err := syscall.Openat(int(d.f.Fd()), name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err == nil {
		return osDir{os.NewFile(uintptr(fd), d.child(name))}, nil
	}
	if errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.ENOTDIR) {
		return nil, &NotDirError{Path: d.child(name), Symlink: isLink(d.child(name))}
	}
	return nil, &fs.PathError{Op: "openat", Path: d.child(name), Err: err}
}

// isLink only words a message: the walk itself has already refused.
func isLink(path string) bool {
	_, err := os.Readlink(path)
	return err == nil
}

// Mkdir implements Dir.
func (d osDir) Mkdir(name string, perm fs.FileMode) (Dir, error) {
	if err := syscall.Mkdirat(int(d.f.Fd()), name, uint32(perm.Perm())); err != nil {
		return nil, &fs.PathError{Op: "mkdirat", Path: d.child(name), Err: err}
	}
	return d.Open(name)
}

func (d osDir) Chown(uid, gid int) error     { return d.f.Chown(uid, gid) }
func (d osDir) Chmod(mode fs.FileMode) error { return d.f.Chmod(mode) }
func (d osDir) Stat() (fs.FileInfo, error)   { return d.f.Stat() }
func (d osDir) Close() error                 { return d.f.Close() }
