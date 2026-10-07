// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// authorize applies the privilege rule (Р5) right after the flags are
// parsed, before the config is read: only the service.user key is peeked
// at, to know who the service user is.
func authorize(deps hostDeps, words string, opts hostOptions, mutating, needsUser bool) (hostsetup.Principal, *repoinit.Failure) {
	return hostsetup.Authorize(hostsetup.Request{
		EUID:        deps.euid,
		ServiceUser: config.PeekServiceUser(opts.configPath),
		Lookup:      deps.lookupUser,
		Mutating:    mutating,
		NeedsUser:   needsUser,
		Command:     strings.Join(append([]string{words}, opts.args...), " "),
	})
}

// runAs is who restic runs as: the service user under sudo, the caller
// when it is the service user already (Р6).
func runAs(who hostsetup.Principal) *restic.RunAs {
	if who.Role != hostsetup.RoleRoot {
		return nil
	}
	return &restic.RunAs{UID: who.Service.UID, GID: who.Service.GID}
}

// report prints a refusal of the command and returns its exit code.
func report(stderr io.Writer, words string, f *repoinit.Failure) int {
	_, _ = fmt.Fprintf(stderr, "sard-agent %s: %s\n", words, f)
	return repoClassCodes[f.Class]
}

// ownedWriter is writeNew that, when the command runs as root, hands the
// file to the service user before it has its name (Р24, ADR 0048): a
// temporary file gets the owner and the mode, then is linked to the final
// path, which fails if something is there already, exactly as writeNew
// does. The final path is never root's.
func ownedWriter(deps hostDeps, who hostsetup.Principal) func(path string, data []byte) error {
	if who.Role != hostsetup.RoleRoot {
		return deps.writeNew
	}
	attrs := hostsetup.Attrs{UID: int(who.Service.UID), GID: int(who.Service.GID), Mode: 0o600}
	return func(path string, data []byte) error {
		tmp, err := hostsetup.StageFile(deps.fs, path, data, attrs)
		if err != nil {
			return err
		}
		return hostsetup.CommitNewFile(deps.fs, tmp, path)
	}
}

// hostCmd is what a command that changes the host carries from step to step.
type hostCmd struct {
	words          string
	deps           hostDeps
	stdout, stderr io.Writer
	opts           hostOptions
	who            hostsetup.Principal
	layout         hostsetup.Layout
	cfg            config.Config
}

func newHostCmd(words string, opts hostOptions, who hostsetup.Principal, stdout, stderr io.Writer, deps hostDeps) *hostCmd {
	return &hostCmd{words: words, deps: deps, stdout: stdout, stderr: stderr, opts: opts, who: who, layout: hostsetup.Layout{Config: opts.configPath}}
}

func (c *hostCmd) fail(f *repoinit.Failure) int { return report(c.stderr, c.words, f) }

// load reads the config again (the first read is before the lock, the
// second under it); a problem is a usage error (В6).
func (c *hostCmd) load() *repoinit.Failure {
	cfg, err := config.Load(c.opts.configPath)
	if err != nil {
		return usageFailureOf("reading config %s: %v", c.opts.configPath, err)
	}
	c.cfg = cfg
	return nil
}

func usageFailureOf(format string, args ...any) *repoinit.Failure {
	return &repoinit.Failure{Class: repoinit.ClassUsage, Detail: fmt.Sprintf(format, args...)}
}

// serviceOwner is the owner of what the service must be able to use.
func (c *hostCmd) serviceOwner(mode os.FileMode) hostsetup.Attrs {
	return hostsetup.Attrs{UID: int(c.who.Service.UID), GID: int(c.who.Service.GID), Mode: mode}
}

// fragmentOwner is the owner of a fragment (Р3): root's, readable by the
// service, which does not change its own settings.
func (c *hostCmd) fragmentOwner() hostsetup.Attrs {
	return hostsetup.Attrs{UID: 0, GID: int(c.who.Service.GID), Mode: 0o640}
}

// lock makes agent.d and takes the lock of config changes (Р10).
func (c *hostCmd) lock() (func(), *repoinit.Failure) {
	if _, err := hostsetup.EnsureDir(c.deps.fs, c.layout.FragmentDir(), hostsetup.Attrs{UID: 0, GID: int(c.who.Service.GID), Mode: 0o750}); err != nil {
		return nil, writeFailed(err)
	}
	return hostsetup.LockConfig(c.deps.openLock, c.layout)
}

// writeFailed is CONFIG_WRITE: the message names the file or directory.
func writeFailed(err error) *repoinit.Failure {
	return repoinit.Fail(repoinit.ConfigWrite, "%v", err)
}

// record writes the audit line of a change (Р9).
func (c *hostCmd) record(kind, name, action string) {
	pname, puid := c.deps.processUser()
	who := hostsetup.ActorFrom(c.deps.getenv, pname, puid)
	hostsetup.Record(c.stderr, c.deps.openAudit, who, kind, name, action)
}

// apply makes the change take effect (Р8) and tells the operator how.
func (c *hostCmd) apply() *repoinit.Failure {
	return hostsetup.Applier{
		Systemd:   c.deps.systemd,
		FS:        c.deps.fs,
		StateDir:  executorStateDir(c.cfg.Executor.StateDir),
		NoRestart: c.opts.noRestart,
	}.Apply(c.stdout)
}

// sourceOptions are the secret source flags of secret set.
func sourceOptions(o hostOptions) hostsetup.SourceOptions {
	return hostsetup.SourceOptions{Stdin: o.stdin, File: o.fromFile, Flags: hostsetup.SourceFlags{Stdin: "--stdin", File: "--from-file"}}
}

// readSource reads the value; the terminal is asked only when no flag was given.
func (c *hostCmd) readSource(o hostsetup.SourceOptions) ([]byte, *repoinit.Failure) {
	src := hostsetup.Source{Stdin: c.deps.stdin, Open: c.deps.openFile}
	if c.deps.terminal != nil && !o.Stdin && o.File == "" {
		src.Terminal = c.deps.terminal(c.stderr)
	}
	return src.Read(o)
}
