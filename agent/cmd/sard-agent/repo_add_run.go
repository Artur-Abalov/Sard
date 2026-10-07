// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// addState is what "repo add" knows about the repository it connects.
type addState struct {
	name, url string
	binary    string
	// provided is the password the operator gave (nil: none yet).
	provided []byte
	// generated is the password this command made up.
	generated string
	id        string
	// attached: the repository existed, it was not created.
	attached bool
}

func (s *addState) final(c *hostCmd) string { return c.layout.PasswordFile(s.name) }

// passwordSource are the flags that give the password of an existing repository (Н4).
func passwordSource(o hostOptions) hostsetup.SourceOptions {
	return hostsetup.SourceOptions{
		Stdin: o.passwordStdin, File: o.passwordFromFile,
		Flags:   hostsetup.SourceFlags{Stdin: "--password-stdin", File: "--password-from-file"},
		Subject: "the password of repository " + o.name,
	}
}

// checkAddress is Р14 from the name to the path: the name, the kind of the
// address (only a local path in A8a, Р17) and the path. It returns the
// path as it will be written to the fragment.
func (c *hostCmd) checkAddress() (string, *refusal.Failure) {
	if f := hostsetup.CheckName("repository", c.opts.name); f != nil {
		return "", f
	}
	if kind := (config.Repository{URL: c.opts.address}).Backend(); kind != "local" {
		return "", refusal.Fail(refusal.BackendNotSupported, "the address is of kind %q: for now only a local path is supported, an absolute path of a directory on this host", kind)
	}
	if hasControlCharacter(c.opts.address) {
		return "", refusal.Fail(refusal.LocalPathInvalid, "%q holds a control character", c.opts.address)
	}
	return c.checkLocalPath(c.opts.address)
}

func (c *hostCmd) checkLocalPath(path string) (string, *refusal.Failure) {
	if !filepath.IsAbs(path) {
		return "", refusal.Fail(refusal.LocalPathInvalid, "%q is not an absolute path", path)
	}
	path = filepath.Clean(path)
	d, _, err := hostsetup.OpenDir(c.deps.fs, path, nil)
	if err == nil {
		_ = d.Close()
		return path, nil
	}
	return path, pathRefusal(path, err)
}

// pathRefusal tells why the walk of a path stopped: a component that is
// missing is no refusal (the command creates it); a link or a file in the
// way is, and so is a path that cannot be looked at.
func pathRefusal(path string, err error) *refusal.Failure {
	var notDir *hostsetup.NotDirError
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case errors.As(err, &notDir):
		return refusal.Fail(refusal.LocalPathInvalid, "%s", notDir)
	}
	return refusal.Fail(refusal.LocalPathInvalid, "%s cannot be looked at: %v", path, err)
}

// repository is the repository of the config by name.
func (c *hostCmd) repository(name string) config.Repository {
	return hostsetup.RepositoryNamed(c.cfg, name)
}

// runRepoAdd is "sard-agent repo add <name> <address>": args excludes "add".
// The checks come in the order of Р14.
func runRepoAdd(ctx context.Context, args []string, stdout, stderr io.Writer, deps hostDeps) int {
	opts, code := parseRepoFlags("add", args, stderr, deps)
	if code != exitOK {
		return code
	}
	if f := hostsetup.CheckSources(passwordSource(opts)); f != nil {
		return report(stderr, "repo add", f)
	}
	who, f := authorize(deps, "repo add", opts, true, true)
	if f != nil {
		return report(stderr, "repo add", f)
	}
	c := newHostCmd("repo add", opts, who, stdout, stderr, deps)
	url, f := c.checkAddress()
	if f != nil {
		return c.fail(f)
	}
	ctx, cancel := repoContext(ctx, deps.clock, opts.timeout)
	defer cancel(nil)
	return c.addChecked(ctx, url)
}

// addChecked goes on once the address is sound: config, conflicts, restic,
// the password flags, and the unchanged case.
func (c *hostCmd) addChecked(ctx context.Context, url string) int {
	if f := c.load(); f != nil {
		return c.fail(f)
	}
	plan, f := hostsetup.PlanRepository(c.cfg, c.layout, c.opts.name, url)
	if f != nil {
		return c.fail(f)
	}
	return c.addPlanned(ctx, url, plan)
}

// addPlanned checks restic, reads the password flags and handles the
// unchanged case; anything else is connected under the locks.
func (c *hostCmd) addPlanned(ctx context.Context, url string, plan hostsetup.AddPlan) int {
	binary, err := checkRestic(ctx, c.cfg, c.opts.configPath, c.deps.executable, c.deps.exec, runAs(c.who))
	if err != nil {
		return reportRepoError(ctx, c.stderr, "add", err)
	}
	st := &addState{name: c.opts.name, url: url, binary: binary}
	if f := c.readGivenPassword(st); f != nil {
		return c.fail(f)
	}
	if plan == hostsetup.AddUnchanged && c.reportUnchanged(ctx, st) {
		return exitOK
	}
	return c.addLocked(ctx, st)
}

// readGivenPassword reads the password the flags name; the terminal is
// asked later, and only for a repository that exists.
func (c *hostCmd) readGivenPassword(st *addState) *refusal.Failure {
	o := passwordSource(c.opts)
	if !o.Stdin && o.File == "" {
		return nil
	}
	value, f := c.readSource(o)
	st.provided = value
	return f
}

// restic is the wrapper for the repository opened with the password file path.
func (c *hostCmd) resticFor(st *addState, passwordFile string) (repoinit.Target, restic.Repository) {
	repo := config.Repository{Name: st.name, URL: st.url, PasswordFile: passwordFile}
	target := repoTarget(repo, repoinit.Checked{}, string(st.provided), st.generated)
	return target, newRestic(c.cfg, st.binary, c.deps, repo, runAs(c.who))
}

// hasControlCharacter: a line break in a path would start a new line of the
// systemd drop-in.
func hasControlCharacter(path string) bool {
	return strings.ContainsFunc(path, func(r rune) bool { return r < 0x20 || r == 0x7f })
}
