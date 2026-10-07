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
	if f := c.load(); f != nil {
		return c.fail(f)
	}
	what, f := planRemoval("repository", opts.name, c.cfg.RepositorySource(opts.name), c.layout.RepositoryFragment(opts.name))
	if f != nil {
		return c.fail(f)
	}
	if what == nothingToRemove {
		_, _ = fmt.Fprintf(stdout, "Repository %q is not connected: nothing to remove.\n", opts.name)
		return exitOK
	}
	return c.removeRepositoryLocked(opts.name)
}

func (c *hostCmd) removeRepositoryLocked(name string) int {
	unlock, f := c.lock()
	if f != nil {
		return c.fail(f)
	}
	defer unlock()
	if f := c.load(); f != nil {
		return c.fail(f)
	}
	what, f := planRemoval("repository", name, c.cfg.RepositorySource(name), c.layout.RepositoryFragment(name))
	if f != nil {
		return c.fail(f)
	}
	if what == nothingToRemove {
		_, _ = fmt.Fprintf(c.stdout, "Repository %q is not connected: nothing to remove.\n", name)
		return exitOK
	}
	repo := c.repository(name)
	if err := c.removeRepositoryFiles(repo.Name, repo.EnvFile); err != nil {
		return c.fail(writeFailed(err))
	}
	c.record("repository", name, "removed")
	c.printRemoved(name)
	return c.finish()
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
}
