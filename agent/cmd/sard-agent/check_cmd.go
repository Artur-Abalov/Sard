// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// sard-agent check (A8d-2): docs/specs/agent/host-check.feature. One line per
// check of the host, in the order of Ч10. The decisions about what a fact
// means are agent/internal/hostcheck; check_cmd.go (the run, the order, what
// depends on what), check_files.go (config, permissions, certificate,
// secrets), check_host.go (restic, plugins, SELinux, the service) and
// check_repos.go (the repositories) collect the facts.
package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
)

func isCheckCommand(args []string) bool { return len(args) > 0 && args[0] == "check" }

// runCheckCommand is "sard-agent check ...": args excludes the "check" word.
func runCheckCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return runCheck(ctx, args, stdout, stderr, productionHostDeps())
}

func runCheck(ctx context.Context, args []string, stdout, stderr io.Writer, deps hostDeps) int {
	if hasHelpFlag(args) {
		printCheckHelp(stdout)
		return exitOK
	}
	opts, code := parseFlags(checkSpec, args, stderr, deps.defaultConfig)
	if code != exitOK {
		return code
	}
	who, f := authorize(deps, "check", opts, false, false)
	if f != nil {
		return report(stderr, "check", f)
	}
	c := newHostCheck(ctx, deps, opts, who, stdout)
	c.run()
	if f := repoinit.Interruption(ctx); f != nil {
		return report(stderr, "check", f)
	}
	c.finish()
	return exitOfWorst(c.printer.Worst())
}

// hostCheck is one run of sard-agent check.
type hostCheck struct {
	ctx     context.Context
	deps    hostDeps
	opts    hostOptions
	who     hostsetup.Principal
	stdout  io.Writer
	printer *hostcheck.Printer
	now     time.Time
	// failed are the identifiers of the checks that ended in an ERROR.
	failed map[string]bool
	// cfg is the config once config.load and config.names passed; partial is
	// what could be read of the main file when they did not.
	cfg     config.Config
	partial config.Config
	// binary is the restic that restic.binary found.
	binary string
	// needsRoot is set when a check was left out for want of root (Ч20);
	// denied is the file a check was refused (EACCES) in the middle of a line.
	needsRoot bool
	denied    string
}

func newHostCheck(ctx context.Context, deps hostDeps, opts hostOptions, who hostsetup.Principal, stdout io.Writer) *hostCheck {
	return &hostCheck{ctx: ctx, deps: deps, opts: opts, who: who, stdout: stdout,
		printer: hostcheck.NewPrinter(stdout, opts.json), now: deps.clock.Now(), failed: map[string]bool{}}
}

// emit prints a line and notes a failure.
func (c *hostCheck) emit(l hostcheck.Line) {
	if l.Status == hostcheck.Error {
		c.failed[l.Check] = true
	}
	c.printer.Print(l)
}

// blocked is the first of the checks that failed, "" if none did.
func (c *hostCheck) blocked(needs []string) string {
	for _, id := range needs {
		if c.failed[id] {
			return id
		}
	}
	return ""
}

// step makes a check unless a check it needs failed.
func (c *hostCheck) step(check string, needs []string, do func() hostcheck.Line) {
	if failed := c.blocked(needs); failed != "" {
		c.emit(hostcheck.NotCheckedLine(check, "", failed))
		return
	}
	c.emit(do())
}

// needConfig are the checks every check that reads the config needs to pass.
var needConfig = []string{hostcheck.CheckConfigLoad, hostcheck.CheckConfigNames}

func (c *hostCheck) run() {
	c.step(hostcheck.CheckServiceUser, nil, c.serviceUser)
	c.configLines()
	c.step(hostcheck.CheckConfigReferences, needConfig, c.references)
	c.step(hostcheck.CheckPermissions, needConfig, c.permissions)
	c.step(hostcheck.CheckResticBinary, nil, c.resticBinary)
	c.tlsLines()
	c.serverLines()
	c.repositoryLines()
	c.secretLines()
	c.pluginLines()
	c.emit(hostcheck.RestoreLocationsLine())
	c.selinuxLines()
	c.serviceLines()
}

// finish prints the summary, and the line about root if a check needed it.
func (c *hostCheck) finish() {
	c.printer.Finish()
	if c.needsRoot && !c.opts.json {
		_, _ = fmt.Fprintln(c.stdout, "some checks need root: run sudo sard-agent check")
	}
}

// guarded runs a check that reads files; one the service user was refused
// is a check that needs root (Ч20).
func (c *hostCheck) guarded(check, subject string, do func() hostcheck.Line) hostcheck.Line {
	c.denied = ""
	l := do()
	if c.denied == "" {
		return l
	}
	c.needsRoot = true
	return hostcheck.NeedsRootLine(check, subject, "cannot read "+c.denied)
}

// read is deps.readFile that remembers a refusal of the file system.
func (c *hostCheck) read(name string) ([]byte, error) {
	data, err := c.deps.readFile(name)
	c.noteDenied(name, err)
	return data, err
}

// noteDenied: for the service user a file it cannot read is a file for root.
func (c *hostCheck) noteDenied(path string, err error) {
	if err != nil && c.who.Role == hostsetup.RoleService && isPermission(err) {
		c.denied = path
	}
}

func (c *hostCheck) serviceUser() hostcheck.Line {
	found := c.who.Service.Name != ""
	name := c.who.Service.Name
	if !found {
		name = config.PeekServiceUser(c.opts.configPath)
	}
	return hostcheck.ServiceUserLine(name, c.who.Service.UID, found)
}

func (c *hostCheck) stateDir() string { return executorStateDir(c.configured().Executor.StateDir) }

// configured is the config of this run, or what could be read of it.
func (c *hostCheck) configured() config.Config {
	if c.configOK() {
		return c.cfg
	}
	return c.partial
}

func (c *hostCheck) configOK() bool { return c.blocked(needConfig) == "" }

// configBlock is the config check that failed, "" if the config is good.
func (c *hostCheck) configBlock() string { return c.blocked(needConfig) }
