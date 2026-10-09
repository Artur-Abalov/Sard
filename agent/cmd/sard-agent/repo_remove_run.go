// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
)

// runRepoRemove is "sard-agent repo remove <name>": args excludes "remove".
// The data in the storage and the password file stay; restic is not
// called (Р19).
func runRepoRemove(_ context.Context, args []string, stdout, stderr io.Writer, deps hostDeps) int {
	opts, code := parseRepoFlags("remove", args, stderr, deps)
	if code != exitOK {
		return code
	}
	who, f := authorize(deps, "repo remove", opts, true, true)
	if f != nil {
		return report(stderr, "repo remove", f)
	}
	c := newHostCmd("repo remove", opts, who, stdout, stderr, deps)
	name := opts.name
	return c.remove(removeSpec{
		kind: "repository", name: name, own: c.layout.RepositoryFragment(name),
		source: func() string { return c.cfg.RepositorySource(name) },
		files: func() error {
			return c.removeRepositoryFiles(name, c.repository(name).EnvFile)
		},
		summary: func() { c.printRemoved(name) },
	})
}

// removeRepositoryFiles removes the fragment first, then the env_file when
// it is one this command's family made (in the secrets directory) and
// nothing else refers to it, then the systemd drop-in (Р19).
func (c *hostCmd) removeRepositoryFiles(name, envFile string) error {
	if _, err := hostsetup.RemoveFile(c.deps.fs, c.layout.RepositoryFragment(name)); err != nil {
		return err
	}
	if c.ownsEnvFile(name, envFile) {
		if _, err := hostsetup.RemoveFile(c.deps.fs, envFile); err != nil {
			return err
		}
	}
	return c.removeDropIn(name)
}

// removeDropIn removes the drop-in of the repository and has systemd read
// the unit files again.
func (c *hostCmd) removeDropIn(name string) error {
	removed, err := hostsetup.DropIn{FS: c.deps.fs, Dir: c.deps.dropInDir}.Remove(name)
	if err != nil || !removed || !c.deps.systemd.Present() {
		return err
	}
	if f := hostsetup.Reload(c.deps.systemd); f != nil {
		return errors.New(f.Error())
	}
	return nil
}

// ownsEnvFile: the env_file lies in the secrets directory and no other
// key of the config uses it.
func (c *hostCmd) ownsEnvFile(name, envFile string) bool {
	if envFile == "" || filepath.Dir(envFile) != c.layout.SecretsDir() || strings.HasPrefix(filepath.Base(envFile), ".") {
		return false
	}
	return hostsetup.ReferencedBy(c.cfg, envFile, hostsetup.RepositoryKey(name, "env_file")) == ""
}

func (c *hostCmd) printRemoved(name string) {
	_, _ = fmt.Fprintf(c.stdout, "Repository %q removed from this host's configuration.\n", name)
	_, _ = fmt.Fprintf(c.stdout, "  The data in the storage was not deleted, and neither was the password file %s:\n  adding the same address again attaches the repository with it.\n", c.layout.PasswordFile(name))
	_, _ = fmt.Fprintf(c.stdout, "WARNING: sources on the server that refer to %q will now be refused.\n", name)
	if repo := c.repository(name); repo.Backend() == "sftp" {
		c.printSFTPRemoved(repo.URL)
	}
}
