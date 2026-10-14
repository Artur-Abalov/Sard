// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
)

// inspect walks the destination from restore.dir, one component at a time
// and without following a link (ВК4): a link or a file in the way and a
// mount point are TARGET_INVALID; what exists at the end must be an empty
// directory. Nothing is changed.
func (a *attempt) inspect() *Failure {
	root, _, err := hostsetup.OpenDir(a.e.opts.FS, a.e.opts.Dir, nil)
	if errors.Is(err, fs.ErrNotExist) {
		return nil // no restore directory yet: nothing of the destination exists
	}
	if err != nil {
		return invalid(err)
	}
	defer func() { _ = root.Close() }()
	dest, f := a.walk(root)
	if f != nil || dest == nil {
		return f
	}
	defer func() { _ = dest.Close() }()
	return a.requireEmpty(dest)
}

// walk opens the components of the destination below root; nil if one does
// not exist. Directories it opened and passes are closed, root is not.
func (a *attempt) walk(root hostsetup.Dir) (hostsetup.Dir, *Failure) {
	rootInfo, err := root.Stat()
	if err != nil {
		return nil, invalid(err)
	}
	cur := root
	for _, name := range strings.Split(a.destination, string(filepath.Separator)) {
		next, f := descend(cur, name, rootInfo)
		if cur != root {
			_ = cur.Close()
		}
		if next == nil {
			return nil, f
		}
		cur = next
	}
	return cur, nil
}

// descend opens the component name of cur: nil with a failure if it cannot
// be used, nil without one if it does not exist.
func descend(cur hostsetup.Dir, name string, rootInfo fs.FileInfo) (hostsetup.Dir, *Failure) {
	next, err := cur.Open(name)
	if err != nil {
		return nil, missingOrInvalid(err)
	}
	if f := sameDevice(next, rootInfo); f != nil {
		_ = next.Close()
		return nil, f
	}
	return next, nil
}

// missingOrInvalid: a component that does not exist ends the walk quietly.
func missingOrInvalid(err error) *Failure {
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return invalid(err)
}

// requireEmpty: a destination that exists is an empty directory (ВВ12).
func (a *attempt) requireEmpty(dest hostsetup.Dir) *Failure {
	names, err := dest.Names()
	if err != nil {
		return invalid(err)
	}
	if len(names) > 0 {
		return &Failure{Reason: refusal.TargetNotEmpty, Stage: StageCheck, Detail: "the destination " + a.destinationPath() + " is not empty: restore into a new or an empty directory"}
	}
	return nil
}

// sameDevice: the directory is on the device of the restore directory; one
// on another is a mount point, and the move would not be one rename.
func sameDevice(d hostsetup.Dir, root fs.FileInfo) *Failure {
	info, err := d.Stat()
	if err != nil {
		return invalid(err)
	}
	if !hostsetup.SameDevice(root, info) {
		return &Failure{Reason: refusal.TargetInvalid, Stage: StageCheck, Detail: "mount point inside the restore directory: " + d.Path() + " is on another file system"}
	}
	return nil
}

// invalid is the failure for a path that cannot be used as a destination.
func invalid(err error) *Failure {
	var notDir *hostsetup.NotDirError
	detail := err.Error()
	switch {
	case errors.As(err, &notDir) && notDir.Symlink:
		detail = notDir.Path + " is a symbolic link; the destination must not cross one"
	case errors.As(err, &notDir):
		detail = notDir.Path + " is not a directory"
	}
	return &Failure{Reason: refusal.TargetInvalid, Stage: StageCheck, Detail: detail}
}
