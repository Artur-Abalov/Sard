// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:build linux

package hostsetup

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"
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

// CreateFile implements Dir.
func (d osDir) CreateFile(name string, perm fs.FileMode) (File, error) {
	fd, err := syscall.Openat(int(d.f.Fd()), name, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_NOCTTY|syscall.O_CLOEXEC, uint32(perm.Perm()))
	if err != nil {
		return nil, &fs.PathError{Op: "openat", Path: d.child(name), Err: err}
	}
	return os.NewFile(uintptr(fd), d.child(name)), nil
}

// OpenFile implements Dir: O_NOFOLLOW so a link is ELOOP, O_NONBLOCK so a
// FIFO nobody writes to does not hang the open.
func (d osDir) OpenFile(name string) (ReadFile, error) {
	fd, err := syscall.Openat(int(d.f.Fd()), name, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, &fs.PathError{Op: "openat", Path: d.child(name), Err: err}
	}
	return os.NewFile(uintptr(fd), d.child(name)), nil
}

// oPath asks for a descriptor that only names the file: no read, no write,
// no block on a FIFO, and with O_NOFOLLOW a link is itself.
const oPath = 0x200000

// Lstat implements Dir.
func (d osDir) Lstat(name string) (fs.FileInfo, error) {
	fd, err := syscall.Openat(int(d.f.Fd()), name, oPath|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, &fs.PathError{Op: "lstat", Path: d.child(name), Err: err}
	}
	f := os.NewFile(uintptr(fd), d.child(name))
	defer func() { _ = f.Close() }()
	return f.Stat()
}

// Rename implements Dir.
func (d osDir) Rename(oldName, newName string) error {
	if err := syscall.Renameat(int(d.f.Fd()), oldName, int(d.f.Fd()), newName); err != nil {
		return &fs.PathError{Op: "renameat", Path: d.child(oldName), Err: err}
	}
	return nil
}

// Remove implements Dir.
func (d osDir) Remove(name string) error {
	if err := syscall.Unlinkat(int(d.f.Fd()), name); err != nil {
		return &fs.PathError{Op: "unlinkat", Path: d.child(name), Err: err}
	}
	return nil
}

// Sync implements Dir.
func (d osDir) Sync() error { return d.f.Sync() }

// Names implements Dir.
func (d osDir) Names() ([]string, error) { return d.f.Readdirnames(-1) }

// Lock implements Dir.
func (d osDir) Lock() error {
	err := syscall.Flock(int(d.f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return ErrBusy
	}
	return err
}

// Handle implements Dir.
func (d osDir) Handle() *os.File { return d.f }

// renameNoReplace is RENAME_NOREPLACE of renameat2.
const renameNoReplace = 1

// atRemoveDir is AT_REMOVEDIR of unlinkat: the syscall package's Unlinkat
// takes no flags.
const atRemoveDir = 0x200

// RenameNoReplace implements Dir. renameat2 is called by its number: the
// syscall package has none for amd64 (see renameat2_*.go).
func (d osDir) RenameNoReplace(oldName string, newDir Dir, newName string) error {
	from, err := syscall.BytePtrFromString(oldName)
	if err != nil {
		return err
	}
	to, err := syscall.BytePtrFromString(newName)
	if err != nil {
		return err
	}
	target := newDir.Handle()
	_, _, errno := syscall.Syscall6(sysRenameat2, d.f.Fd(), uintptr(unsafe.Pointer(from)), target.Fd(), uintptr(unsafe.Pointer(to)), renameNoReplace, 0)
	runtime.KeepAlive(target)
	if errno != 0 {
		return &fs.PathError{Op: "renameat2", Path: newDir.Path() + "/" + newName, Err: renameError(oldName, errno)}
	}
	return nil
}

// Move implements Dir.
func (d osDir) Move(oldName string, newDir Dir, newName string) error {
	target := newDir.Handle()
	err := syscall.Renameat(int(d.f.Fd()), oldName, int(target.Fd()), newName)
	runtime.KeepAlive(target)
	if err != nil {
		return &fs.PathError{Op: "renameat", Path: newDir.Path() + "/" + newName, Err: renameError(oldName, err)}
	}
	return nil
}

// renameError is the error of a move: EXDEV, a mount point on the way, is
// ErrCrossDevice; any other stays as it is.
func renameError(name string, err error) error {
	if errors.Is(err, syscall.EXDEV) {
		return fmt.Errorf("%s: %w", name, ErrCrossDevice)
	}
	return err
}

// RemoveDir implements Dir.
func (d osDir) RemoveDir(name string) error {
	p, err := syscall.BytePtrFromString(name)
	if err != nil {
		return err
	}
	_, _, errno := syscall.Syscall(syscall.SYS_UNLINKAT, d.f.Fd(), uintptr(unsafe.Pointer(p)), atRemoveDir)
	if errno != 0 {
		return &fs.PathError{Op: "unlinkat", Path: d.child(name), Err: errno}
	}
	return nil
}
