// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package files

import (
	"io"
	"io/fs"
	"os"
)

// FS is the part of the host's file system the plugin looks at. It never
// reads the content of a file.
type FS interface {
	// Lstat describes the path itself, not what a symbolic link points to.
	Lstat(name string) (fs.FileInfo, error)
	// Readlink returns where a symbolic link points.
	Readlink(name string) (string, error)
	// Open opens a file or directory for reading.
	Open(name string) (File, error)
}

// File is an open file or directory.
type File interface {
	io.Closer
	// Readdirnames lists at most n names of a directory; io.EOF when empty.
	Readdirnames(n int) ([]string, error)
}

// osFS is the file system of the host.
type osFS struct{}

func (osFS) Lstat(name string) (fs.FileInfo, error) { return os.Lstat(name) }
func (osFS) Readlink(name string) (string, error)   { return os.Readlink(name) }
func (osFS) Open(name string) (File, error)         { return os.Open(name) }
