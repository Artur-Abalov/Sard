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
	// AllowRoot lets the target be "/".
	AllowRoot bool
	// Avoid are the directories the target must neither be nor lie in:
	// the local repositories and the cache of restic.
	Avoid []string
}

// StagingPrefix starts the name of the directory in the target where restic
// writes before the files are moved into the target (Р73).
const StagingPrefix = ".sard-restore-"

// lostAndFound is the directory a fresh ext4 volume holds; a target with it
// alone is empty (Н30).
const lostAndFound = "lost+found"

// CheckTarget judges the target and returns it held: absolute, no link or
// file in the way, not inside a directory to avoid, and empty (a new one is).
// An existing target is opened by walking its path without following a link
// and is locked from the walk on, before it is judged empty, so that a second
// restore into it is TARGET_BUSY and not TARGET_NOT_EMPTY (Р79). A missing one
// is made by Create. The root is refused without AllowRoot and is never empty:
// restoring into it is A8c-2b (Р74). Nothing is changed.
func CheckTarget(fsys hostsetup.FS, t Target) (*Held, *refusal.Failure) {
	if !filepath.IsAbs(t.Path) {
		return nil, refusal.Fail(refusal.TargetInvalid, "--target %q must be an absolute path", t.Path)
	}
	clean := filepath.Clean(t.Path)
	if clean == "/" {
		return nil, checkRoot(t)
	}
	held := &Held{Path: clean, fsys: fsys}
	if f := held.judge(t.Avoid); f != nil {
		held.Release()
		return nil, f
	}
	return held, nil
}

// judge walks and locks the target if it exists and finds it fit.
func (h *Held) judge(avoid []string) *refusal.Failure {
	if f := h.walk(); f != nil {
		return f
	}
	if f := checkAvoided(h.Path, avoid); f != nil {
		return f
	}
	if h.dir == nil {
		return nil
	}
	return h.checkEmpty()
}

func checkRoot(t Target) *refusal.Failure {
	if !t.AllowRoot {
		return refusal.Fail(refusal.RootTargetRefused, "restoring into the root of the file system / replaces files of this host; add --allow-root-target if that is meant")
	}
	return notEmpty("/")
}

func notEmpty(path string) *refusal.Failure {
	return refusal.Fail(refusal.TargetNotEmpty, "the target %s is not empty; restore into an empty or new directory", path)
}

// walk opens the target by its path from "/" without following a link and
// locks it; a missing target is left for Create.
func (h *Held) walk() *refusal.Failure {
	dir, _, err := hostsetup.OpenDir(h.fsys, h.Path, nil)
	if err != nil {
		return missingOrInvalid(err)
	}
	h.dir = dir
	if err := dir.Lock(); err != nil {
		return lockFailure(err, h.Path)
	}
	return nil
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

// checkEmpty: nothing in the held target, or only an empty lost+found. The
// names are read once: a descriptor reads its directory to the end.
func (h *Held) checkEmpty() *refusal.Failure {
	names, err := h.dir.Names()
	if err != nil {
		return refusal.Fail(refusal.TargetInvalid, "cannot read the target %s: %v", h.Path, err)
	}
	if f := leftover(h.Path, names); f != nil {
		return f
	}
	empty, err := isEmpty(h.dir, names)
	if err != nil {
		return refusal.Fail(refusal.TargetInvalid, "cannot read the target %s: %v", h.Path, err)
	}
	if !empty {
		return notEmpty(h.Path)
	}
	return nil
}

// tempMarker follows the name in .<name>.tmp-<random>, the temporary file of
// a PostgreSQL restore (Р62).
const tempMarker = ".tmp-"

// isRemains: the name of what a restore that was killed leaves: a staging
// directory (Р73) or a temporary file .<name>.tmp-* with a name that is not
// empty (Р88). It is told by the name alone.
func isRemains(name string) bool {
	if strings.HasPrefix(name, StagingPrefix) {
		return true
	}
	rest, dotted := strings.CutPrefix(name, ".")
	return dotted && strings.Index(rest, tempMarker) > 0
}

// leftover: what a restore that was killed leaves makes the target not
// empty, and the refusal names it (Р73, Р88).
func leftover(path string, names []string) *refusal.Failure {
	for _, name := range names {
		if isRemains(name) {
			return refusal.Fail(refusal.TargetNotEmpty, "the target %s is not empty: %s is the remains of an interrupted restore; remove it (rm -rf) and run the command again", path, filepath.Join(path, name))
		}
	}
	return nil
}

// isEmpty: no names, or only an empty lost+found.
func isEmpty(dir hostsetup.Dir, names []string) (bool, error) {
	if len(names) == 0 {
		return true, nil
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

// Create makes what is missing of the target (root:root, 0700) and locks it
// for the restore; a second restore into it is TARGET_BUSY. A target that
// existed is held already. Nothing created stays behind when it fails.
func (h *Held) Create() *refusal.Failure {
	if h.dir != nil {
		return nil
	}
	dir, created, err := hostsetup.OpenDir(h.fsys, h.Path, &hostsetup.Make{Parents: rootOnly, Last: rootOnly})
	h.dir, h.created = dir, created
	if err != nil {
		h.removeCreated()
		return openFailure(err)
	}
	if err := dir.Lock(); err != nil {
		h.Abandon()
		return lockFailure(err, h.Path)
	}
	return nil
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

// Release lets go of the target; it may be called more than once.
func (h *Held) Release() {
	if h.dir != nil {
		_ = h.dir.Close()
	}
}

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
