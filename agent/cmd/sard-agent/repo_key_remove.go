// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoconnect"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/restickeys"
)

// masterKeyNotice is said after every removal (K14, rule 9): removing a key
// does not make the data unreadable to whoever had the key.
const masterKeyNotice = "Note: removing a key does not change the repository master key: anyone who had this key could have extracted the master key and can still decrypt existing backups; to fully revoke access create a new repository."

// runKeyRemove is "sard-agent repo key remove <name> <id>": args excludes
// "remove". The checks come in the order of K14: flags, rights, name, config,
// role, the keys, the key, the last key, the key of this host, confirmation.
func runKeyRemove(ctx context.Context, args []string, stdout, stderr io.Writer, deps hostDeps) int {
	opts, code := parseRepoFlags("key remove", args, stderr, deps)
	if code != exitOK {
		return code
	}
	if err := restickeys.CheckID(opts.address); err != nil {
		return usageFailure(stderr, "repo key remove", err.Error())
	}
	who, f := authorize(deps, "repo key remove", opts, true, true)
	if f != nil {
		return report(stderr, "repo key remove", f)
	}
	ctx, budget := repoconnect.NewBudget(ctx, deps.clock, opts.timeout)
	defer budget.Cancel(nil)
	access, code := prepareKeys(ctx, "key remove", opts, who, stderr, deps)
	if code != exitOK {
		return code
	}
	c := newHostCmd("repo key remove", opts, who, stdout, stderr, deps)
	c.cfg, c.budget = access.cfg, budget
	unlock, f := c.lock()
	if f != nil {
		return c.fail(f)
	}
	defer unlock()
	return c.removeKey(ctx, access)
}

// removeKey finds the key the id names, asks, removes it and tells the operator.
func (c *hostCmd) removeKey(ctx context.Context, access *keyAccess) int {
	keys, f := access.keys(ctx)
	if f != nil {
		return c.fail(f)
	}
	key, found, f := c.keyToRemove(keys)
	switch {
	case f != nil:
		return c.fail(f)
	case !found:
		_, _ = fmt.Fprintf(c.stdout, "nothing to remove: repository %q has no key with the id %s\n", c.opts.name, c.opts.address)
		return exitOK
	}
	if f := c.confirmRemoval(key); f != nil {
		return c.fail(f)
	}
	return c.removeConfirmed(ctx, access, key)
}

// removeConfirmed removes the key, records it, says what removing does not do
// and applies the change when the key was the recovery key.
func (c *hostCmd) removeConfirmed(ctx context.Context, access *keyAccess, key restic.Key) int {
	if f := access.attempt(ctx, access.target(), func(ctx context.Context, cli *restic.CLI) error { return cli.RemoveKey(ctx, key.ID) }); f != nil {
		return c.fail(f)
	}
	tag, _ := restickeys.TagOf(key.UserName)
	c.recordKey("removed", key.ID, tag)
	_, _ = fmt.Fprintf(c.stdout, "Key %s removed from repository %q.\n%s\n", shortID(key.ID), c.opts.name, masterKeyNotice)
	if tag.Purpose != restickeys.Recovery {
		return exitOK
	}
	if f := c.apply(); f != nil {
		return c.fail(f)
	}
	return exitOK
}

// keyToRemove is the key the id prefix names; found is false for no key. The
// last key and the key of this host are refused (K14, rule 8).
func (c *hostCmd) keyToRemove(keys []restic.Key) (key restic.Key, found bool, f *refusal.Failure) {
	matches := restickeys.Match(keys, c.opts.address)
	switch {
	case len(matches) == 0:
		return restic.Key{}, false, nil
	case len(matches) > 1:
		return restic.Key{}, false, refusal.Fail(refusal.KeyIDAmbiguous, "the id %s names %d keys; give more characters: %s", c.opts.address, len(matches), ids(matches))
	case len(keys) == 1:
		return restic.Key{}, false, refusal.Fail(refusal.LastKey, "key %s is the only key of repository %q: without keys the repository cannot be opened", shortID(matches[0].ID), c.opts.name)
	case matches[0].Current:
		return restic.Key{}, false, refusal.Fail(refusal.KeyInUse, "key %s is the key of this host: this host opens the repository with this key, so removing it would lock this host out; run the command on a host that uses another key", shortID(matches[0].ID))
	}
	return matches[0], true, nil
}

// ids are the full ids of the keys, for a message.
func ids(keys []restic.Key) string {
	full := make([]string, len(keys))
	for i, k := range keys {
		full[i] = k.ID
	}
	return strings.Join(full, ", ")
}

// confirmRemoval asks the operator, unless --yes says not to (K14): only the
// answer "yes" removes; without a terminal and without --yes nothing is read.
func (c *hostCmd) confirmRemoval(key restic.Key) *refusal.Failure {
	if c.opts.yes {
		return nil
	}
	var ask func(string) (string, error)
	if c.deps.terminal != nil {
		ask = repoconnect.Confirmation(c.deps.terminal(c.stderr), c.budget)
	}
	if ask == nil {
		return refusal.Fail(refusal.ConfirmationRequired, "removing a key asks for a confirmation, and there is no terminal: give --yes to remove without asking")
	}
	answer, err := ask(removalQuestion(key))
	if err != nil || answer != "yes" {
		return refusal.Fail(refusal.ConfirmationRejected, "the answer was not exactly \"yes\": nothing was removed")
	}
	return nil
}

// removalQuestion shows the key the operator is about to lose (K14).
func removalQuestion(key restic.Key) string {
	purpose, label := keyTagColumns(key)
	return fmt.Sprintf("Remove key %s (purpose %s, label %s, host %s, created %s)? Type yes to remove: ",
		key.ID, purpose, label, dashIfEmpty(key.HostName), key.Created.UTC().Format(time.RFC3339))
}
