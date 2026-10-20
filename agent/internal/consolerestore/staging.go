// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package consolerestore

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
)

// makeStaging makes restore.dir if it is missing, the directory
// .sard-staging in it and <command_id> in that (ВК13), all of the service
// user and mode 0700. A name that exists is not reused.
func (a *attempt) makeStaging() *Failure {
	owner := hostsetup.Make{Parents: a.e.opts.Owner, Last: a.e.opts.Owner}
	root, created, err := hostsetup.OpenDir(a.e.opts.FS, a.e.opts.Dir, &owner)
	a.created = append(a.created, created...)
	if err != nil {
		return a.cannotWrite(a.e.opts.Dir, err)
	}
	a.root = root
	staging, err := a.openStagingDir()
	if err != nil {
		return a.cannotWrite(filepath.Join(a.e.opts.Dir, stagingName), err)
	}
	a.staging = staging
	dir, err := staging.Mkdir(a.req.CommandID, a.e.opts.Owner.Mode)
	if err != nil {
		return a.cannotWrite(a.stagingPath(), err)
	}
	a.stagingMade = true
	a.command = dir
	made, err := dir.Mkdir(rootName, a.e.opts.Owner.Mode)
	if err != nil {
		return a.cannotWrite(filepath.Join(a.stagingPath(), rootName), err)
	}
	_ = made.Close() // only made here; whoever restores opens it again
	return nil
}

// openStagingDir opens .sard-staging, and makes it if it is missing.
func (a *attempt) openStagingDir() (hostsetup.Dir, error) {
	staging, err := a.root.Open(stagingName)
	if !errors.Is(err, fs.ErrNotExist) {
		return staging, err
	}
	staging, err = a.root.Mkdir(stagingName, a.e.opts.Owner.Mode)
	if err == nil {
		a.created = append(a.created, filepath.Join(a.e.opts.Dir, stagingName))
	}
	return staging, err
}

// cannotWrite is the failure for a directory the service cannot make. The
// usual cause is the unit: a path outside StateDirectory is read-only to it.
func (a *attempt) cannotWrite(path string, err error) *Failure {
	return &Failure{
		Reason: refusal.TargetWrite, Stage: StageCheck,
		Detail: fmt.Sprintf("cannot make %s: %v; the restore directory %s must be writable by the service: outside /var/lib/sard-agent add it to ReadWritePaths= of the unit (docs/operations/agent-host-setup.md)", path, err, a.e.opts.Dir),
	}
}
