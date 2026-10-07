// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"fmt"
	"io"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
)

// removal is what a remove command finds in the config.
type removal int

const (
	nothingToRemove removal = iota
	removeOwn
)

// planRemoval: a name defined in the main config or in a fragment this
// command did not write is refused (Р12).
func planRemoval(kind, name, source, own string) (removal, *repoinit.Failure) {
	switch {
	case source == "":
		return nothingToRemove, nil
	case source != own:
		return nothingToRemove, repoinit.Fail(repoinit.DefinedInConfig, "%s %q is defined in %s; commands never change it, edit that file instead", kind, name, source)
	}
	return removeOwn, nil
}

// runSecretRemove is "sard-agent secret remove <name>": args excludes "remove".
func runSecretRemove(_ context.Context, args []string, stdout, stderr io.Writer, deps hostDeps) int {
	opts, code := parseSecretFlags("remove", args, stderr, deps)
	if code != exitOK {
		return code
	}
	who, f := authorize(deps, "secret remove", opts, true, true)
	if f != nil {
		return report(stderr, "secret remove", f)
	}
	c := newHostCmd("secret remove", opts, who, stdout, stderr, deps)
	if f := c.load(); f != nil {
		return c.fail(f)
	}
	what, f := planRemoval("secret", opts.name, c.cfg.SecretSource(opts.name), c.layout.SecretFragment(opts.name))
	if f != nil {
		return c.fail(f)
	}
	if what == nothingToRemove {
		_, _ = fmt.Fprintf(stdout, "Secret %q is not set: nothing to remove.\n", opts.name)
		return exitOK
	}
	return c.removeSecretLocked(opts.name)
}

func (c *hostCmd) removeSecretLocked(name string) int {
	unlock, f := c.lock()
	if f != nil {
		return c.fail(f)
	}
	defer unlock()
	if f := c.load(); f != nil {
		return c.fail(f)
	}
	what, f := planRemoval("secret", name, c.cfg.SecretSource(name), c.layout.SecretFragment(name))
	if f != nil {
		return c.fail(f)
	}
	if what == nothingToRemove {
		_, _ = fmt.Fprintf(c.stdout, "Secret %q is not set: nothing to remove.\n", name)
		return exitOK
	}
	if err := c.removeSecretFiles(name); err != nil {
		return c.fail(writeFailed(err))
	}
	c.record("secret", name, "removed")
	_, _ = fmt.Fprintf(c.stdout, "Secret %q removed.\n", name)
	return c.finish()
}

// removeSecretFiles removes the fragment first: from then on the agent
// does not know the secret; then the value file, unless it is not the one
// this command makes or another key of the config uses it.
func (c *hostCmd) removeSecretFiles(name string) error {
	if _, err := hostsetup.RemoveFile(c.deps.fs, c.layout.SecretFragment(name)); err != nil {
		return err
	}
	valueFile := c.layout.SecretFile(name)
	if c.cfg.Secrets[name] != valueFile || hostsetup.ReferencedBy(c.cfg, valueFile, hostsetup.SecretKey(name)) != "" {
		return nil
	}
	_, err := hostsetup.RemoveFile(c.deps.fs, valueFile)
	return err
}
