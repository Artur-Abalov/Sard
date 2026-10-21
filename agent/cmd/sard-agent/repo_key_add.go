// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"fmt"
	"io"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/restickeys"
)

// keyPassword is shown to the operator when it is created and nowhere else.
const keyPasswordWarning = "WARNING: this password is stored nowhere else; copy it now."

// keyIssue is the key a command issues: its tag, where its password goes.
type keyIssue struct {
	tag restickeys.Tag
	// file is the file the password goes to; "" for the terminal.
	file string
}

// issueFromFlags checks --for, --label and --to-file as far as flags go (K5,
// K7, K8): before the rights, the config and restic.
func issueFromFlags(opts hostOptions, stdoutTerminal bool) (keyIssue, *refusal.Failure) {
	purpose, f := restickeys.ParsePurpose(opts.set["for"], opts.purpose)
	if f != nil {
		return keyIssue{}, f
	}
	if f := restickeys.CheckLabel(purpose, opts.set["label"], opts.label); f != nil {
		return keyIssue{}, f
	}
	if opts.toFile == "" && !stdoutTerminal {
		return keyIssue{}, refusal.Fail(refusal.KeyOutputMissing, "standard output is not a terminal, and the password of the new key is shown once and stored nowhere: give --to-file <absolute path> to have it written to a new file (mode 0600)")
	}
	return keyIssue{tag: restickeys.Tag{Purpose: purpose, Label: opts.label}, file: opts.toFile}, nil
}

// runKeyAdd is "sard-agent repo key add <name> --for <purpose>": args excludes
// "add". The checks come in the order of K14: flags, rights, name, config,
// role, the file of the password, the lock, the keys of the repository.
func runKeyAdd(ctx context.Context, args []string, stdout, stderr io.Writer, deps hostDeps) int {
	opts, code := parseRepoFlags("key add", args, stderr, deps)
	if code != exitOK {
		return code
	}
	issue, f := issueFromFlags(opts, deps.stdoutTerminal)
	if f != nil {
		return report(stderr, "repo key add", f)
	}
	who, f := authorize(deps, "repo key add", opts, true, true)
	if f != nil {
		return report(stderr, "repo key add", f)
	}
	ctx, cancel := repoContext(ctx, deps.clock, opts.timeout)
	defer cancel(nil)
	access, code := prepareKeys(ctx, "key add", opts, who, stderr, deps)
	if code != exitOK {
		return code
	}
	c := newHostCmd("repo key add", opts, who, stdout, stderr, deps)
	c.cfg = access.cfg
	return c.issueKey(ctx, access, issue)
}

// issueKey checks the file, takes the lock and issues the key.
func (c *hostCmd) issueKey(ctx context.Context, access *keyAccess, issue keyIssue) int {
	if issue.file != "" {
		if f := c.checkKeyFile("--to-file", issue.file); f != nil {
			return c.fail(f)
		}
	}
	unlock, f := c.lock()
	if f != nil {
		return c.fail(f)
	}
	defer unlock()
	return c.issueUnderLock(ctx, access, issue)
}

// issueUnderLock reads the keys, refuses or accepts a key that exists already
// (K10), creates the key and tells the operator.
func (c *hostCmd) issueUnderLock(ctx context.Context, access *keyAccess, issue keyIssue) int {
	keys, f := access.keys(ctx)
	if f != nil {
		return c.fail(f)
	}
	if _, found := restickeys.Find(keys, issue.tag); found {
		return c.keyExists(issue.tag)
	}
	password, err := restickeys.NewPassword(c.deps.random)
	if err != nil {
		return c.fail(refusal.Fail(refusal.ResticFailed, "%v", err))
	}
	return c.finishIssue(ctx, access, issue, password)
}

// keyExists: the same purpose and label is a refusal, or with --if-missing
// nothing to do (K10).
func (c *hostCmd) keyExists(tag restickeys.Tag) int {
	if c.opts.ifMissing {
		_, _ = fmt.Fprintf(c.stdout, "Key for %s unchanged: the repository %q has it already.\n", describeTag(tag), c.opts.name)
		return exitOK
	}
	return c.fail(refusal.Fail(refusal.KeyExists, "repository %q has a key for %s already (whoever issued it); give another --label, or --if-missing to leave it as it is", c.opts.name, describeTag(tag)))
}

// describeTag is the purpose and the label for a message.
func describeTag(t restickeys.Tag) string {
	if t.Label == "" {
		return "purpose " + string(t.Purpose)
	}
	return fmt.Sprintf("purpose %s, label %s", t.Purpose, t.Label)
}

// keyIssuer is the one way a key is issued, by repo key add and by the
// recovery key of repo add and repo init: restic key add within
// --connect-timeout, the id of the new key, the password handed over once,
// the audit line and the next steps.
type keyIssuer struct {
	c *hostCmd
	// target describes the repository to the messages; secret is one more
	// value they must not show.
	target func(secret string) repoinit.Target
	// attempt runs one restic call within the time --connect-timeout gives.
	attempt func(ctx context.Context, target repoinit.Target, call func(ctx context.Context, cli *restic.CLI) error) *refusal.Failure
}

// issue makes the key and hands its password over. A call that ended with the
// time, or an interrupt, may have made the key: the message says how to find
// out (K20).
func (k keyIssuer) issue(ctx context.Context, issue keyIssue, password string) *refusal.Failure {
	target := k.target(password)
	if f := k.create(ctx, target, issue.tag, password); f != nil {
		return f
	}
	id := k.newKeyID(ctx, target, issue.tag)
	if f := k.c.showPassword(issue, password, id); f != nil {
		return f
	}
	k.c.recordKey("added", id, issue.tag)
	k.c.printNextSteps(issue.tag)
	return nil
}

// create runs restic key add.
func (k keyIssuer) create(ctx context.Context, target repoinit.Target, tag restickeys.Tag, password string) *refusal.Failure {
	host, err := k.c.deps.hostname()
	if err != nil {
		return refusal.Fail(refusal.ResticFailed, "the name of this host is unknown: %v", err)
	}
	f := k.attempt(ctx, target, func(ctx context.Context, cli *restic.CLI) error {
		return cli.AddKey(ctx, restic.NewKey{User: tag.User(), Host: host, Password: password})
	})
	if f != nil && mayHaveCreated(f.Reason) {
		f.Detail += fmt.Sprintf("; the key may have been created: check with `sudo sard-agent repo key list %s` and remove it with `sudo sard-agent repo key remove %s <id>`", k.c.opts.name, k.c.opts.name)
	}
	return f
}

// mayHaveCreated: a call that ended with a time limit (--timeout, or
// --connect-timeout, which is BACKEND_UNAVAILABLE) or an interrupt may have
// finished the key before it was stopped.
func mayHaveCreated(reason refusal.Reason) bool {
	return reason == refusal.Timeout || reason == refusal.Interrupted || reason == refusal.BackendUnavailable
}

// newKeyID is the id of the key just made: restic does not print it, the
// list does. "" if the list cannot be read now; the key exists anyway.
func (k keyIssuer) newKeyID(ctx context.Context, target repoinit.Target, tag restickeys.Tag) string {
	var keys []restic.Key
	f := k.attempt(ctx, target, func(ctx context.Context, cli *restic.CLI) (err error) {
		keys, err = cli.Keys(ctx)
		return err
	})
	if f != nil {
		_, _ = fmt.Fprintf(k.c.stderr, "warning: the id of the new key is unknown (%s); find it with `sudo sard-agent repo key list %s`\n", f.Detail, k.c.opts.name)
		return ""
	}
	key, _ := restickeys.Find(restickeys.Sorted(keys), tag)
	return key.ID
}

// finishIssue issues the key and applies the change when it matters to the
// server.
func (c *hostCmd) finishIssue(ctx context.Context, access *keyAccess, issue keyIssue, password string) int {
	issuer := keyIssuer{c: c, target: func(secret string) repoinit.Target { return access.target(secret) }, attempt: access.attempt}
	if f := issuer.issue(ctx, issue, password); f != nil {
		return c.fail(f)
	}
	if issue.tag.Purpose != restickeys.Recovery {
		return exitOK
	}
	if f := c.apply(); f != nil {
		return c.fail(f)
	}
	return exitOK
}

// showPassword puts the password in the file or on the screen, once (K7).
func (c *hostCmd) showPassword(issue keyIssue, password, id string) *refusal.Failure {
	if issue.file != "" {
		if f := c.writeKeyFile(issue.file, password); f != nil {
			f.Detail += fmt.Sprintf("; the key %s was created but its password is lost: remove it with `sudo sard-agent repo key remove %s %s` and issue it again", id, c.opts.name, shortID(id))
			return f
		}
		_, _ = fmt.Fprintf(c.stdout, "Key %s issued for repository %q (%s).\nThe password of the new key was written to %s (mode 0600).\n", shortID(id), c.opts.name, describeTag(issue.tag), issue.file)
		return nil
	}
	_, _ = fmt.Fprintf(c.stdout, "Key %s issued for repository %q (%s).\nPassword of the new key: %s\n%s\n", shortID(id), c.opts.name, describeTag(issue.tag), password, keyPasswordWarning)
	return nil
}

// shortID is the id as `key list` prints it.
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// recordKey writes the audit line of a key change (K18).
func (c *hostCmd) recordKey(action, id string, tag restickeys.Tag) {
	pname, puid := c.deps.processUser()
	who := hostsetup.ActorFrom(c.deps.getenv, pname, puid)
	hostsetup.RecordKey(c.stderr, c.deps.openAudit, who, hostsetup.KeyChange{
		Action: action, Repository: c.opts.name, ID: id, Purpose: dashIfEmpty(string(tag.Purpose)), Label: dashIfEmpty(tag.Label)})
}

// printNextSteps tells what to do with the key (K11, K12).
func (c *hostCmd) printNextSteps(tag restickeys.Tag) {
	switch tag.Purpose {
	case restickeys.Verifier:
		c.printVerifierSteps(c.repository(c.opts.name))
	case restickeys.Recovery:
		_, _ = fmt.Fprintln(c.stdout, "\nKeep the recovery key outside this infrastructure (offline, with the owner of the backups): without a recovery key and without the password of this host the backups of this repository cannot be recovered when this host is lost.")
	}
}

// printVerifierSteps is the command for the host of the verifier, with the
// address of the repository without its password (K11).
func (c *hostCmd) printVerifierSteps(repo config.Repository) {
	_, _ = fmt.Fprintf(c.stdout, "\nOn the verifier host run:\n  sudo sard-agent repo add %s %s --password-from-file <file with the password of this key>\n", repo.Name, config.StripPassword(repo.URL))
	_, _ = fmt.Fprintln(c.stdout, "Use storage credentials that are read-only on the verifier: for s3 the --access-key-id of a key that may only read, for sftp a user without the right to write.")
	if repo.Backend() == "local" {
		_, _ = fmt.Fprintln(c.stdout, "This repository is on a local path of this host: the verifier must see the same storage over the network (a share mounted at the same path, for example).")
	}
}
