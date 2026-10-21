// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"errors"
	"io/fs"
	"path/filepath"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
)

// checkKeyFile is every check of the file a password goes to (K8), made
// before restic is asked for anything: an absolute path, walked from "/"
// without following a link, in a directory that exists, with nothing at the
// name. flag names the option for the messages.
func (c *hostCmd) checkKeyFile(flag, path string) *refusal.Failure {
	if !filepath.IsAbs(path) {
		return usageFailureOf("%s %q must be an absolute path", flag, path)
	}
	dir, name := filepath.Dir(path), filepath.Base(path)
	d, _, err := hostsetup.OpenDir(c.deps.fs, dir, nil)
	if f := keyDirFailure(dir, err); f != nil {
		return f
	}
	defer func() { _ = d.Close() }()
	if _, err := d.Lstat(name); err == nil {
		return refusal.Fail(refusal.KeyFileExists, "%s exists already and is never overwritten; choose another name", path)
	}
	return nil
}

// keyDirFailure explains a walk of the directory that failed.
func keyDirFailure(dir string, err error) *refusal.Failure {
	var notDir *hostsetup.NotDirError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &notDir):
		return refusal.Fail(refusal.KeyFileRejected, "%s; the path of the password file is walked without following links", notDir)
	case errors.Is(err, fs.ErrNotExist):
		return refusal.Fail(refusal.KeyFileWrite, "the directory %s does not exist; create it first", dir)
	}
	return refusal.Fail(refusal.KeyFileWrite, "the directory %s cannot be used: %v", dir, err)
}

// keyFileOwner is who the file belongs to: root for a command run as root
// (В10), the service user for a command it runs itself, which cannot give a
// file to root (K15а).
func (c *hostCmd) keyFileOwner() hostsetup.Attrs {
	if c.who.Role == hostsetup.RoleRoot {
		return hostsetup.Attrs{UID: 0, GID: 0, Mode: 0o600}
	}
	return c.serviceOwner(0o600)
}

// writeKeyFile writes the password and a line break to a new file at path
// (K8): in the directory of the file, owner and mode first, then the content,
// then the name, which an existing file keeps.
func (c *hostCmd) writeKeyFile(path, password string) *refusal.Failure {
	tmp, err := hostsetup.StageFile(c.deps.fs, path, []byte(password+"\n"), c.keyFileOwner())
	if err == nil {
		err = hostsetup.CommitNewFile(c.deps.fs, tmp, path)
	}
	if err != nil {
		return refusal.Fail(refusal.KeyFileWrite, "%v", err)
	}
	return nil
}
