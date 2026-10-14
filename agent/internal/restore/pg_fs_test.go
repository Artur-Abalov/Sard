// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restore_test

import (
	"io/fs"
	"strconv"
	"strings"
	"syscall"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
)

// fileBehaviour is what the files that recordingFS makes do: it notes the
// order of what happens to each (a test is not root) and fails a step on demand.
type fileBehaviour struct {
	// events are "chown <name> 0:0", "chmod <name> 600", "write <name>" in order.
	events []string
	// failWrite is the error the write of a file whose name has this prefix fails with.
	failWritePrefix string
	failWrite       error
	// failChown makes the owner change of a file fail; failRename the move to the final name.
	failChown, failRename bool
	// failCreate is the error the making of any file fails with; failFileSync
	// makes the sync of a file fail.
	failCreate   error
	failFileSync bool
	// failWriteLater, if not nil, is the error every write of a file after the
	// first failed one fails with, instead of failWrite.
	failWriteLater error
	failedWrites   int
	// closed are the names of the files that were closed, in order.
	closed []string
}

type recordingFile struct {
	hostsetup.File
	fsys *recordingFS
	name string
}

func (d recordingDir) CreateFile(name string, perm fs.FileMode) (hostsetup.File, error) {
	if err := d.fsys.files.failCreate; err != nil {
		return nil, &fs.PathError{Op: "openat", Path: name, Err: err}
	}
	f, err := d.Dir.CreateFile(name, perm)
	if err != nil {
		return nil, err
	}
	return recordingFile{File: f, fsys: d.fsys, name: name}, nil
}

func (f recordingFile) Chown(uid, gid int) error {
	if f.fsys.files.failChown {
		return &fs.PathError{Op: "fchown", Path: f.name, Err: syscall.EPERM}
	}
	f.fsys.note("chown " + f.name + " " + strconv.Itoa(uid) + ":" + strconv.Itoa(gid))
	return nil
}

func (f recordingFile) Chmod(mode fs.FileMode) error {
	f.fsys.note("chmod " + f.name + " " + mode.String())
	return f.File.Chmod(mode)
}

func (f recordingFile) Write(p []byte) (int, error) {
	f.fsys.note("write " + f.name)
	if f.fsys.files.failWrite != nil && strings.HasPrefix(f.name, f.fsys.files.failWritePrefix) {
		err := f.fsys.files.failWrite
		if f.fsys.files.failedWrites > 0 && f.fsys.files.failWriteLater != nil {
			err = f.fsys.files.failWriteLater
		}
		f.fsys.files.failedWrites++
		return 0, &fs.PathError{Op: "write", Path: f.name, Err: err}
	}
	return f.File.Write(p)
}

func (fsys *recordingFS) note(event string) {
	fsys.mu.Lock()
	defer fsys.mu.Unlock()
	fsys.files.events = append(fsys.files.events, event)
}

func (f recordingFile) Sync() error {
	if f.fsys.files.failFileSync {
		return &fs.PathError{Op: "fsync", Path: f.name, Err: syscall.EIO}
	}
	return f.File.Sync()
}

func (f recordingFile) Close() error {
	f.fsys.mu.Lock()
	f.fsys.files.closed = append(f.fsys.files.closed, f.name)
	f.fsys.mu.Unlock()
	return f.File.Close()
}
