// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
)

// removal is what a remove command finds in the config.
type removal int

const (
	nothingToRemove removal = iota
	removeOwn
)

// planRemoval: a name defined in the main config or in a fragment this
// command did not write is refused (Р12).
func planRemoval(kind, name, source, own string) (removal, *refusal.Failure) {
	switch {
	case source == "":
		return nothingToRemove, nil
	case source != own:
		return nothingToRemove, refusal.Fail(refusal.DefinedInConfig, "%s %q is defined in %s; commands never change it, edit that file instead", kind, name, source)
	}
	return removeOwn, nil
}

// removeSpec is what a remove command takes away: the name, who defines
// it and what goes with it.
type removeSpec struct {
	kind, name string
	// own is the fragment the add command writes for the name.
	own string
	// source is the file of the config that defines the name now.
	source func() string
	// files removes the files that belong to the name.
	files func() error
	// summary tells the operator what was removed.
	summary func()
}

// planFor decides, from the config as it is now, what the command is to do.
func (sp removeSpec) planFor() (removal, *refusal.Failure) {
	return planRemoval(sp.kind, sp.name, sp.source(), sp.own)
}

// remove is the pipeline of every remove command: config, decision, lock,
// the decision again under the lock, files, audit, applying.
func (c *hostCmd) remove(sp removeSpec) int {
	if f := c.load(); f != nil {
		return c.fail(f)
	}
	what, f := sp.planFor()
	switch {
	case f != nil:
		return c.fail(f)
	case what == nothingToRemove:
		return c.nothingToRemove(sp)
	}
	return c.removeLocked(sp)
}

func (c *hostCmd) nothingToRemove(sp removeSpec) int {
	_, _ = fmt.Fprintf(c.stdout, "%s %q is not set up: nothing to remove.\n", capitalised(sp.kind), sp.name)
	return exitOK
}

func capitalised(s string) string { return strings.ToUpper(s[:1]) + s[1:] }

func (c *hostCmd) removeLocked(sp removeSpec) int {
	var what removal
	return c.underConfigLock(
		func() (f *refusal.Failure) { what, f = sp.planFor(); return f },
		func() int {
			if what == nothingToRemove {
				return c.nothingToRemove(sp)
			}
			return c.removeFiles(sp)
		})
}

func (c *hostCmd) removeFiles(sp removeSpec) int {
	if err := sp.files(); err != nil {
		return c.fail(writeFailed(err))
	}
	c.record(sp.kind, sp.name, "removed")
	sp.summary()
	return c.finish()
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
	name := opts.name
	return c.remove(removeSpec{
		kind: "secret", name: name, own: c.layout.SecretFragment(name),
		source:  func() string { return c.cfg.SecretSource(name) },
		files:   func() error { return c.removeSecretFiles(name) },
		summary: func() { _, _ = fmt.Fprintf(c.stdout, "Secret %q removed.\n", name) },
	})
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
