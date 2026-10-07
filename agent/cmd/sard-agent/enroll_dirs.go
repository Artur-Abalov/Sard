// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/Artur-Abalov/sard/agent/internal/enroll"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
)

// enrollOwner is who the identity files belong to: the service user when
// the command runs as root (Р25), nobody in particular otherwise.
func enrollOwner(who hostsetup.Principal, deps enrollDeps) *enroll.Owner {
	if who.Role != hostsetup.RoleRoot {
		return nil
	}
	return &enroll.Owner{UID: int(who.Service.UID), GID: int(who.Service.GID), Chown: deps.chownFile}
}

// tlsDir is the directory of one tls.* file and the key that names it.
type tlsDir struct{ key, dir string }

func tlsDirs(files enroll.Files) []tlsDir {
	return []tlsDir{
		{"tls.key_file", filepath.Dir(files.KeyFile)},
		{"tls.cert_file", filepath.Dir(files.CertFile)},
		{"tls.ca_file", filepath.Dir(files.CAFile)},
	}
}

// ensureTLSDirs is Р25а: under root the last directory of a tls.* path is
// created if it is missing (the service user's, 0700); a missing parent as
// well is a write error and nothing is made. It returns the directories it
// made. From the service user nothing is created (В12).
func ensureTLSDirs(files enroll.Files, who hostsetup.Principal, deps enrollDeps, stderr io.Writer) ([]string, int) {
	if who.Role != hostsetup.RoleRoot {
		return nil, exitOK
	}
	missing, err := missingTLSDirs(deps.fs, files)
	if err != nil {
		return nil, reportEnrollError(stderr, err)
	}
	attrs := hostsetup.Attrs{UID: int(who.Service.UID), GID: int(who.Service.GID), Mode: 0o700}
	var created []string
	for _, dir := range missing {
		if _, err := hostsetup.EnsureDir(deps.fs, dir, attrs); err != nil {
			removeTLSDirs(deps.fs, created)
			return nil, reportEnrollError(stderr, enroll.NewWriteError(fmt.Sprintf("creating the directory %s failed", dir), err))
		}
		created = append(created, dir)
	}
	return created, exitOK
}

// missingTLSDirs lists the directories to make, each once, in the order of
// the keys; a directory whose parent is missing too is an error naming the
// key and the first missing directory of the path.
func missingTLSDirs(fsys hostsetup.FS, files enroll.Files) ([]string, error) {
	var missing []string
	for _, d := range tlsDirs(files) {
		if exists(fsys, d.dir) || contains(missing, d.dir) {
			continue
		}
		if !exists(fsys, filepath.Dir(d.dir)) {
			first := firstMissing(fsys, d.dir)
			return nil, enroll.NewWriteError(fmt.Sprintf("%s: the directory %s does not exist; only the last directory of a tls.* path is created", d.key, first), fs.ErrNotExist)
		}
		missing = append(missing, d.dir)
	}
	return missing, nil
}

func exists(fsys hostsetup.FS, path string) bool {
	_, err := fsys.Stat(path)
	return err == nil || !errors.Is(err, fs.ErrNotExist)
}

func contains(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}

// firstMissing is the outermost directory of path that does not exist.
func firstMissing(fsys hostsetup.FS, path string) string {
	first := path
	for dir := filepath.Dir(path); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if exists(fsys, dir) {
			break
		}
		first = dir
	}
	return first
}

// removeTLSDirs takes back the directories this run made, last first; one
// that holds something else stays.
func removeTLSDirs(fsys hostsetup.FS, dirs []string) {
	for i := len(dirs) - 1; i >= 0; i-- {
		_ = fsys.Remove(dirs[i])
	}
}

// chownOpenFile is fchown: the file, not whatever its name leads to.
func chownOpenFile(f *os.File, uid, gid int) error { return f.Chown(uid, gid) }
