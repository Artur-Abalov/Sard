// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restore

import (
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
)

// Target is where repo restore writes (Р59).
type Target struct {
	// Path is the target as the operator typed it.
	Path string
	// Overwrite lets the files of the snapshot replace the files of a
	// target that is not empty; AllowRoot lets the target be "/".
	Overwrite, AllowRoot bool
	// Avoid are the directories the target must neither be nor lie in:
	// the local repositories and the cache of restic.
	Avoid []string
}

// lostAndFound is the directory a fresh ext4 volume holds; a target with it
// alone is empty (Н30).
const lostAndFound = "lost+found"

// CheckTarget judges the target without changing anything and returns its
// clean path: absolute, no link or file in the way, not inside a directory
// to avoid, and empty unless Overwrite is given. The root is refused without
// AllowRoot and is never empty (Н31).
func CheckTarget(fsys hostsetup.FS, t Target) (string, *refusal.Failure) {
	if !filepath.IsAbs(t.Path) {
		return "", refusal.Fail(refusal.TargetInvalid, "--target %q must be an absolute path", t.Path)
	}
	clean := filepath.Clean(t.Path)
	if clean == "/" {
		return clean, checkRoot(t)
	}
	return clean, checkExisting(fsys, clean, t)
}

// checkExisting: what is there of the target can be restored into.
func checkExisting(fsys hostsetup.FS, path string, t Target) *refusal.Failure {
	empty, f := walkTarget(fsys, path)
	if f == nil {
		f = checkAvoided(path, t.Avoid)
	}
	if f == nil && !empty && !t.Overwrite {
		f = notEmpty(path)
	}
	return f
}

func checkRoot(t Target) *refusal.Failure {
	switch {
	case !t.AllowRoot:
		return refusal.Fail(refusal.RootTargetRefused, "restoring into the root of the file system / replaces files of this host; add --allow-root-target and --overwrite if that is meant")
	case !t.Overwrite:
		return notEmpty("/")
	}
	return nil
}

func notEmpty(path string) *refusal.Failure {
	return refusal.Fail(refusal.TargetNotEmpty, "the target %s is not empty; restore into an empty directory, or add --overwrite to replace the files of the snapshot there (other files stay)", path)
}

// walkTarget walks the path from "/" without following a link: whether the
// target is empty (a missing one is), or why it cannot be used.
func walkTarget(fsys hostsetup.FS, path string) (bool, *refusal.Failure) {
	dir, _, err := hostsetup.OpenDir(fsys, path, nil)
	if err != nil {
		return true, missingOrInvalid(err)
	}
	defer func() { _ = dir.Close() }()
	empty, err := isEmpty(dir)
	if err != nil {
		return false, refusal.Fail(refusal.TargetInvalid, "cannot read the target %s: %v", path, err)
	}
	return empty, nil
}

// missingOrInvalid: a missing component is fine (it is created later); a
// link, a file or any other error in the way is not.
func missingOrInvalid(err error) *refusal.Failure {
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return refusal.Fail(refusal.TargetInvalid, "%v", err)
}

func checkAvoided(path string, avoid []string) *refusal.Failure {
	for _, dir := range avoid {
		if path == dir || strings.HasPrefix(path, dir+"/") {
			return refusal.Fail(refusal.TargetInvalid, "the target %s is %s, which holds the repository or the cache of restic; restore somewhere else", path, dir)
		}
	}
	return nil
}

// isEmpty: nothing in the directory, or only an empty lost+found.
func isEmpty(dir hostsetup.Dir) (bool, error) {
	names, err := dir.Names()
	if err != nil || len(names) == 0 {
		return err == nil, err
	}
	if !slices.Equal(names, []string{lostAndFound}) {
		return false, nil
	}
	child, err := dir.Open(lostAndFound)
	if err != nil {
		return false, nil // a file or a link: not the directory of a volume
	}
	defer func() { _ = child.Close() }()
	inside, err := child.Names()
	return err == nil && len(inside) == 0, err
}

// Held is the target while the restore into it runs: opened by its
// descriptor and locked.
type Held struct {
	Path    string
	dir     hostsetup.Dir
	fsys    hostsetup.FS
	created []string
}

// rootOnly is the owner and the mode of a directory this command creates,
// whatever the umask.
var rootOnly = hostsetup.Attrs{UID: 0, GID: 0, Mode: 0o700}

// OpenTarget creates what is missing of the target (root:root, 0700) and
// locks it for the restore; a second restore into it is TARGET_BUSY.
// Nothing created stays behind when it fails.
func OpenTarget(fsys hostsetup.FS, path string) (*Held, *refusal.Failure) {
	dir, created, err := hostsetup.OpenDir(fsys, path, &hostsetup.Make{Parents: rootOnly, Last: rootOnly})
	held := &Held{Path: path, dir: dir, fsys: fsys, created: created}
	if err != nil {
		held.removeCreated()
		return nil, openFailure(err)
	}
	if err := dir.Lock(); err != nil {
		held.Abandon()
		return nil, lockFailure(err, path)
	}
	return held, nil
}

func openFailure(err error) *refusal.Failure {
	var notDir *hostsetup.NotDirError
	if errors.As(err, &notDir) {
		return refusal.Fail(refusal.TargetInvalid, "%v", err)
	}
	return refusal.Fail(refusal.TargetWrite, "cannot create the target: %v", err)
}

func lockFailure(err error, path string) *refusal.Failure {
	if errors.Is(err, hostsetup.ErrBusy) {
		return refusal.Fail(refusal.TargetBusy, "another restore into %s is running; wait for it and run the command again", path)
	}
	return refusal.Fail(refusal.TargetWrite, "cannot lock the target %s: %v", path, err)
}

// Dir is the held target directory.
func (h *Held) Dir() hostsetup.Dir { return h.dir }

// Release lets go of the target.
func (h *Held) Release() { _ = h.dir.Close() }

// Abandon lets go of the target and removes the directories this run
// created that are still empty (as Р25а).
func (h *Held) Abandon() {
	h.Release()
	h.removeCreated()
}

func (h *Held) removeCreated() {
	for _, p := range slices.Backward(h.created) {
		_ = h.fsys.Remove(p) // rmdir: a directory with files stays
	}
}
