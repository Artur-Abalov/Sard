// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/restickeys"
)

// The recovery key of a new repository (V3, K15, K15а): repo add and repo init
// issue it right after restic init, and decide before init where its password
// goes. A repository that exists already gets none.

// recoveryPlan is where the password of the recovery key goes: nowhere (the
// operator refused the key), a file, or the terminal.
type recoveryPlan struct {
	skip bool
	file string
}

// checkRecoveryFlags are the checks of the flags alone: the two flags exclude
// each other, and the file is an absolute path.
func checkRecoveryFlags(o hostOptions) *refusal.Failure {
	switch {
	case o.recoveryKeyFile != "" && o.noRecoveryKey:
		return usageFailureOf("--recovery-key-file and --no-recovery-key exclude each other: give the file the password goes to, or refuse the recovery key")
	case o.recoveryKeyFile != "" && !filepath.IsAbs(o.recoveryKeyFile):
		return usageFailureOf("--recovery-key-file %q must be an absolute path", o.recoveryKeyFile)
	}
	return nil
}

// planRecovery chooses the output before anything is created: the refusal,
// else the file (its path checked now), else the terminal; none of them is
// RECOVERY_KEY_OUTPUT_MISSING.
func (c *hostCmd) planRecovery() (recoveryPlan, *refusal.Failure) {
	switch {
	case c.opts.noRecoveryKey:
		return recoveryPlan{skip: true}, nil
	case c.opts.recoveryKeyFile != "":
		return recoveryPlan{file: c.opts.recoveryKeyFile}, c.checkKeyFile("--recovery-key-file", c.opts.recoveryKeyFile)
	case c.deps.stdoutTerminal:
		return recoveryPlan{}, nil
	}
	return recoveryPlan{}, refusal.Fail(refusal.RecoveryKeyOutputMissing, "standard output is not a terminal, and the password of the recovery key of a new repository is shown once and stored nowhere: give --recovery-key-file <absolute path> to have it written to a new file (mode 0600), or --no-recovery-key to go without one (losing this host then means losing the backups)")
}

// decideRecovery chooses the output once for the command; a failure ends it
// before the repository is made.
func (c *hostCmd) decideRecovery() *refusal.Failure {
	if c.recovery != nil {
		return nil
	}
	plan, f := c.planRecovery()
	if f == nil {
		c.recovery = &plan
	}
	return f
}

// skipRecovery tells the operator what the refusal costs and records it (K15).
func (c *hostCmd) skipRecovery() {
	_, _ = fmt.Fprintf(c.stderr, "warning: no recovery key was issued for repository %q: without a recovery key, losing this host means losing the backups\n", c.opts.name)
	pname, puid := c.deps.processUser()
	hostsetup.RecordRecoveryKeySkipped(c.stderr, c.deps.openAudit, hostsetup.ActorFrom(c.deps.getenv, pname, puid), c.opts.name)
}

// issueRecovery issues the recovery key of the repository just created with
// restic init, or records that it was refused. A failure is RECOVERY_KEY_FAILED
// whatever its cause: the repository exists, only the key is missing.
func (c *hostCmd) issueRecovery(ctx context.Context, plan recoveryPlan, cli *restic.CLI, target repoinit.Target) *refusal.Failure {
	if plan.skip {
		c.skipRecovery()
		return nil
	}
	password, err := restickeys.NewPassword(c.deps.random)
	if err == nil {
		err = c.addRecoveryKey(ctx, plan, cli, withSecret(target, password), password)
	}
	if err == nil {
		return nil
	}
	return refusal.Fail(refusal.RecoveryKeyFailed, "the repository was created, but its recovery key was not issued: %v; issue it with `sudo sard-agent repo key add %s --for recovery`", err, c.opts.name)
}

// addRecoveryKey makes the key and hands its password over.
func (c *hostCmd) addRecoveryKey(ctx context.Context, plan recoveryPlan, cli *restic.CLI, target repoinit.Target, password string) error {
	host, err := c.deps.hostname()
	if err != nil {
		return fmt.Errorf("the name of this host is unknown: %w", err)
	}
	tag := restickeys.Tag{Purpose: restickeys.Recovery}
	if err := cli.AddKey(ctx, restic.NewKey{User: tag.User(), Host: host, Password: password}); err != nil {
		return errors.New(repoinit.FromRestic(ctx, err, target).Detail)
	}
	id := recoveryKeyID(ctx, cli, tag)
	if f := c.showPassword(keyIssue{tag: tag, file: plan.file}, password, id); f != nil {
		return errors.New(f.Detail)
	}
	c.recordKey("added", id, tag)
	c.printNextSteps(tag)
	return nil
}

// recoveryKeyID is the id of the key just made; "" if the list cannot be read.
func recoveryKeyID(ctx context.Context, cli *restic.CLI, tag restickeys.Tag) string {
	keys, err := cli.Keys(ctx)
	if err != nil {
		return ""
	}
	key, _ := restickeys.Find(keys, tag)
	return key.ID
}

// withSecret is the target whose messages hide one more value.
func withSecret(target repoinit.Target, secret string) repoinit.Target {
	scrub := target.Scrub
	target.Scrub = func(s string) string { return scrub(strings.ReplaceAll(s, secret, "***")) }
	return target
}

// localRepositoryIsNew says that nothing is stored at the local path yet: a
// repository there has its config file.
func (c *hostCmd) localRepositoryIsNew(path string) bool {
	_, err := c.deps.fs.Stat(filepath.Join(path, "config"))
	return errors.Is(err, fs.ErrNotExist)
}
