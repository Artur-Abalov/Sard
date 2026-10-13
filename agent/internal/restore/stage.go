// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restore

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
)

// Staging is the directory .sard-restore-<random> in the held target, with a
// subdirectory root, where restic writes (Р73). Both belong to root and have
// mode 0700, so nobody else can put a link in the way of root's writes. When
// restic is done the entries of root are moved into the target and the
// staging directory is removed.
type Staging struct {
	target hostsetup.Dir
	name   string
	dir    hostsetup.Dir
	root   hostsetup.Dir
	// path is the staging directory as the operator names it.
	path string
}

// maxStagingNames is how many random names Stage tries.
const maxStagingNames = 8

// Stage makes the staging directory in the held target: mkdirat, opened
// without following a link, owner and mode through the descriptor and judged
// by fstat; and inside it the directory root the same way. random gives the
// part of the name that is random; a name that is taken is replaced by
// another. Nothing stays behind when it fails.
func (h *Held) Stage(random func() string) (*Staging, *refusal.Failure) {
	for range maxStagingNames {
		name := StagingPrefix + random()
		dir, err := h.dir.Mkdir(name, rootOnly.Mode)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, refusal.Fail(refusal.TargetWrite, "cannot make the temporary directory in the target %s: %v", h.Path, err)
		}
		s := &Staging{target: h.dir, name: name, dir: dir, path: filepath.Join(h.Path, name)}
		if f := s.prepare(); f != nil {
			_ = s.Discard()
			return nil, f
		}
		return s, nil
	}
	return nil, refusal.Fail(refusal.TargetWrite, "cannot find a free name for the temporary directory in the target %s", h.Path)
}

// prepare makes the staging directory fit and makes root in it.
func (s *Staging) prepare() *refusal.Failure {
	if f := fit(s.dir, s.path); f != nil {
		return f
	}
	root, err := s.dir.Mkdir("root", rootOnly.Mode)
	if err != nil {
		return refusal.Fail(refusal.TargetWrite, "cannot make %s/root: %v", s.path, err)
	}
	s.root = root
	return fit(root, s.path+"/root")
}

// fit gives the directory its owner and mode through its descriptor, then
// looks at what it really is.
func fit(dir hostsetup.Dir, path string) *refusal.Failure {
	if err := errors.Join(dir.Chown(rootOnly.UID, rootOnly.GID), dir.Chmod(rootOnly.Mode)); err != nil {
		return refusal.Fail(refusal.TargetWrite, "cannot set the owner and the mode of %s: %v", path, err)
	}
	info, err := dir.Stat()
	if err != nil {
		return refusal.Fail(refusal.TargetWrite, "cannot look at %s: %v", path, err)
	}
	if !ownedByRoot(info) {
		uid, _, _ := hostsetup.OwnerOf(info)
		return refusal.Fail(refusal.TargetWrite, "%s is not a directory of root with mode 0700 (owner %d, mode %v); it is not used", path, uid, info.Mode())
	}
	return nil
}

// ownedByRoot: a directory of uid 0 with mode 0700.
func ownedByRoot(info fs.FileInfo) bool {
	uid, _, found := hostsetup.OwnerOf(info)
	return found && uid == 0 && info.IsDir() && info.Mode().Perm() == rootOnly.Mode
}

// Path is the staging directory, for messages.
func (s *Staging) Path() string { return s.path }

// Dir is the staging directory, which restic inherits by its descriptor.
func (s *Staging) Dir() hostsetup.Dir { return s.dir }

// Commit moves every entry of root into the target, one by one and without
// replacing: a name that exists there was not there when the target was
// judged, and is not touched (TARGET_WRITE; what was moved before it stays,
// the rest is removed with the staging directory). A mount point on the way
// is TARGET_INVALID. The staging directory is removed in every case.
func (s *Staging) Commit() *refusal.Failure {
	f := s.move()
	if err := s.Discard(); err != nil && f == nil {
		f = refusal.Fail(refusal.TargetWrite, "cannot remove the temporary directory %s: %v; remove it before the next restore", s.path, err)
	}
	return f
}

func (s *Staging) move() *refusal.Failure {
	names, err := s.root.Names()
	if err != nil {
		return refusal.Fail(refusal.TargetWrite, "cannot read %s/root: %v", s.path, err)
	}
	for _, name := range names {
		if err := s.root.RenameNoReplace(name, s.target, name); err != nil {
			return s.moveFailure(name, err)
		}
	}
	if err := s.target.Sync(); err != nil {
		return refusal.Fail(refusal.TargetWrite, "cannot sync the target %s: %v", filepath.Dir(s.path), err)
	}
	return nil
}

func (s *Staging) moveFailure(name string, err error) *refusal.Failure {
	target := filepath.Dir(s.path)
	entry := filepath.Join(target, name)
	switch {
	case errors.Is(err, fs.ErrExist):
		return refusal.Fail(refusal.TargetWrite, "%s turned up in the target while restic worked and is not replaced; the entries moved before it stay in the target, the rest was removed with %s", entry, s.name)
	case errors.Is(err, hostsetup.ErrCrossDevice):
		return refusal.Fail(refusal.TargetInvalid, "mount point inside the target: %s is on another file system than %s; the entries moved before it stay in the target, the rest was removed with %s", entry, s.name, s.name)
	}
	return refusal.Fail(refusal.TargetWrite, "cannot move %s into the target: %v; the entries moved before it stay in the target, the rest was removed with %s", entry, err, s.name)
}

// Discard lets go of the staging directory and removes it with everything in
// it, by descriptors and without following a link.
func (s *Staging) Discard() error {
	if s.root != nil {
		_ = s.root.Close()
	}
	_ = s.dir.Close()
	if err := hostsetup.RemoveTree(s.target, s.name); err != nil {
		return fmt.Errorf("%s: %w", s.path, err)
	}
	return nil
}

// finish ends a staging directory after a failure of restic: what was
// restored stays when only some files could not be (RESTORE_INCOMPLETE), and
// is removed with the staging directory in any other case.
func (s *Staging) finish(f *refusal.Failure) *refusal.Failure {
	if f.Reason == refusal.RestoreIncomplete {
		if moved := s.Commit(); moved != nil {
			return moved
		}
		return f
	}
	if err := s.Discard(); err != nil {
		f.Detail += "; the temporary directory could not be removed: " + err.Error()
	}
	return f
}
