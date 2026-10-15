// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"fmt"
	"io"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
)

// exitWarnings is the code of a check that found warnings and no errors
// (A8d, Ч9); with errors the code is exitAgentError.
const exitWarnings = 8

// The identifiers of the checks of one repository, in their order (Ч3). They
// are stable between versions (Ч10).
const (
	checkStorage     = "repository.storage"
	checkCredentials = "repository.credentials"
	checkInitialized = "repository.initialized"
	checkPassword    = "repository.password"
	checkID          = "repository.id"
	checkLocks       = "repository.locks"
	checkIntegrity   = "repository.integrity"
)

// repoCheck is one run of "sard-agent repo check".
type repoCheck struct {
	ctx     context.Context
	opts    hostOptions
	who     hostsetup.Principal
	deps    hostDeps
	cfg     config.Config
	binary  string
	printer *hostcheck.Printer
}

// runRepoCheck is "sard-agent repo check [<name>]": args excludes "check".
// The order is that of Р69: flags, rights, config, name, restic, and then the
// checks, each repository after the other.
func runRepoCheck(ctx context.Context, args []string, stdout, stderr io.Writer, deps hostDeps) int {
	opts, code := parseRepoFlags("check", args, stderr, deps)
	if code != exitOK {
		return code
	}
	who, f := authorize(deps, "repo check", opts, false, true)
	if f != nil {
		return report(stderr, "repo check", f)
	}
	cfg, targets, code := checkTargets(opts, stderr)
	if code != exitOK {
		return code
	}
	binary, err := checkRestic(ctx, cfg, opts.configPath, deps.executable, deps.exec, runAs(who))
	if err != nil {
		return reportRepoError(ctx, stderr, "check", err)
	}
	c := &repoCheck{ctx: ctx, opts: opts, who: who, deps: deps, cfg: cfg, binary: binary, printer: hostcheck.NewPrinter(stdout, opts.json)}
	return c.run(targets, stderr)
}

// checkTargets are the positions in the config of the repositories to check:
// the one named, or all of them in the order of the config (Ч8).
func checkTargets(opts hostOptions, stderr io.Writer) (config.Config, []int, int) {
	cfg, code := loadRepoConfig("check", opts.configPath, stderr)
	if code != exitOK {
		return cfg, nil, code
	}
	if opts.name == "" {
		all := make([]int, len(cfg.Repositories))
		for i := range all {
			all[i] = i
		}
		return cfg, all, exitOK
	}
	_, index, f := findRepository(cfg, opts.name, opts.configPath)
	if f != nil {
		return cfg, nil, report(stderr, "repo check", f)
	}
	return cfg, []int{index}, exitOK
}

func (c *repoCheck) run(targets []int, stderr io.Writer) int {
	if len(targets) == 0 {
		c.printer.Print(noRepositoriesLine())
	}
	for _, index := range targets {
		if c.interrupted() {
			break
		}
		c.checkOne(index)
	}
	if f := repoinit.Interruption(c.ctx); f != nil {
		return report(stderr, "repo check", f)
	}
	c.printer.Finish()
	return exitOfWorst(c.printer.Worst())
}

func (c *repoCheck) interrupted() bool { return c.ctx.Err() != nil }

// exitOfWorst is Ч9: 1 with an error, 8 with a warning, else 0.
func exitOfWorst(worst hostcheck.Status) int {
	switch worst {
	case hostcheck.Error:
		return exitAgentError
	case hostcheck.Warn:
		return exitWarnings
	}
	return exitOK
}

// checkOne checks one repository within the time --timeout allows it.
func (c *repoCheck) checkOne(index int) {
	ctx, cancel := repoContext(c.ctx, c.deps.clock, c.opts.timeout)
	defer cancel(nil)
	newRepoSteps(c, ctx, index).run()
}

// failureLine is the line of a check that a refusal ended: the message names
// the reason, the hint says what to do about it.
func failureLine(check, subject string, f *refusal.Failure, hint string) hostcheck.Line {
	return hostcheck.Line{Check: check, Subject: subject, Status: hostcheck.Error, Message: named(f).Error(), Hint: hint}
}

// skipLine is the line of a check that was not made because an earlier one
// failed (Ч3).
func skipLine(check, subject, failed string) hostcheck.Line {
	return hostcheck.Line{Check: check, Subject: subject, Status: hostcheck.Skip,
		Message: fmt.Sprintf("not checked: %s failed", failed),
		Hint:    fmt.Sprintf("fix %s first, then run `sudo sard-agent repo check %s` again", failed, subject)}
}

// noRepositoriesLine is the one line of a host with no repository: OK, with a
// note that says how to add one (a host may have only sources that need none).
func noRepositoriesLine() hostcheck.Line {
	return hostcheck.Line{Check: checkStorage, Status: hostcheck.OK, Message: "no repositories configured; add one with `sudo sard-agent repo add <name> <address>`"}
}
