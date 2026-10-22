// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// sard-agent repo key add, list and remove (V3): docs/specs/agent/restic-keys.feature.
// The decisions (what the user field of a key says, which warnings a list
// earns, which key an id names) are agent/internal/restickeys; repo_key_run.go
// (dispatch, the access to the keys, key list), repo_key_add.go and
// repo_key_remove.go are the commands.
package main

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoconnect"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/restickeys"
	"github.com/Artur-Abalov/sard/agent/internal/restore"
	"github.com/Artur-Abalov/sard/agent/internal/role"
)

// runRepoKey is "sard-agent repo key ...": args excludes "key".
func runRepoKey(ctx context.Context, args []string, stdout, stderr io.Writer, deps hostDeps) int {
	var run hostCommand
	if len(args) > 0 {
		run = map[string]hostCommand{"add": runKeyAdd, "list": runKeyList, "remove": runKeyRemove}[args[0]]
	}
	if run == nil {
		return usageFailure(stderr, "repo key", "want a subcommand: add, list or remove")
	}
	return run(ctx, args[1:], stdout, stderr, deps)
}

// keyAccess is the repository whose keys a command reads or changes.
type keyAccess struct {
	snapshotsState
	opts hostOptions
	deps hostDeps
	log  repoconnect.Log
}

// prepareKeys is what the key commands do before the storage is touched:
// the config, the name, the role of the host when the command changes the
// keys (K17), restic and the files of the repository.
func prepareKeys(ctx context.Context, sub string, opts hostOptions, who hostsetup.Principal, stderr io.Writer, deps hostDeps) (*keyAccess, int) {
	mutating := sub != "key list"
	st, code := prepareRepository(ctx, sub, opts, stderr, deps, func(cfg config.Config, _ config.Repository) (hostsetup.Principal, int) {
		if mutating {
			if f := refuseOnVerifier(deps, cfg, "issue or remove keys"); f != nil {
				return who, report(stderr, "repo "+sub, f)
			}
		}
		return who, exitOK
	}, noTools)
	if code != exitOK {
		return nil, code
	}
	return &keyAccess{snapshotsState: st, opts: opts, deps: deps}, exitOK
}

// isVerifier says that the certificate of this host gives it the role of a
// verifier, which only reads (V3, K17).
func isVerifier(deps hostDeps, cfg config.Config) bool {
	return deps.hostRole(cfg.TLS.CertFile) == role.Verifier
}

// refuseOnVerifier is the refusal of every command that changes a repository
// (K17); words say what the command does. A new command that changes a
// repository calls it, and keys_verifier_test.go lists it.
func refuseOnVerifier(deps hostDeps, cfg config.Config, words string) *refusal.Failure {
	if !isVerifier(deps, cfg) {
		return nil
	}
	return refusal.Fail(refusal.VerifierReadOnly, "this host is a verifier: a verifier only reads repositories, so it cannot %s; run the command on a source host", words)
}

// cli is restic for the repository; its stderr is kept for the message of a
// storage that does not answer.
func (a *keyAccess) cli() *restic.CLI {
	return newRestic(a.cfg, a.binary, a.deps, a.repo, runAs(a.who)).WithStderr(&a.log)
}

// target describes the repository to the messages; secrets are values they must not show.
func (a *keyAccess) target(secrets ...string) repoinit.Target {
	return repoconnect.TargetFor(a.repo, a.checked.EnvAssignments, append(passwordValues(a.repo, a.who, a.deps), secrets...)...)
}

// attempt runs one restic call within the time --connect-timeout gives the
// first access to the storage.
func (a *keyAccess) attempt(ctx context.Context, target repoinit.Target, call func(ctx context.Context, cli *restic.CLI) error) *refusal.Failure {
	cli := a.cli()
	bound := repoconnect.Bound{Clock: a.deps.clock, Timeout: a.opts.connectTimeout}
	return bound.Attempt(ctx, target, &a.log,
		func(limited context.Context) error { return call(limited, cli) },
		func(ctx context.Context, err error) *refusal.Failure { return restore.ListFailure(ctx, err, target) })
}

// keys reads the keys of the repository.
func (a *keyAccess) keys(ctx context.Context) ([]restic.Key, *refusal.Failure) {
	var keys []restic.Key
	f := a.attempt(ctx, a.target(), func(ctx context.Context, cli *restic.CLI) (err error) {
		keys, err = cli.Keys(ctx)
		return err
	})
	return restickeys.Sorted(keys), f
}

// runKeyList is "sard-agent repo key list <name>": args excludes "list".
func runKeyList(ctx context.Context, args []string, stdout, stderr io.Writer, deps hostDeps) int {
	opts, code := parseRepoFlags("key list", args, stderr, deps)
	if code != exitOK {
		return code
	}
	who, f := authorize(deps, "repo key list", opts, false, true)
	if f != nil {
		return report(stderr, "repo key list", f)
	}
	ctx, cancel := repoContext(ctx, deps.clock, opts.timeout)
	defer cancel(nil)
	access, code := prepareKeys(ctx, "key list", opts, who, stderr, deps)
	if code != exitOK {
		return code
	}
	keys, f := access.keys(ctx)
	if f != nil {
		return report(stderr, "repo key list", f)
	}
	printKeys(stdout, stderr, opts, keys)
	return exitOK
}

// printKeys prints the list, then the warnings on stderr (K13); a JSON
// list carries its warnings as codes instead.
func printKeys(stdout, stderr io.Writer, opts hostOptions, keys []restic.Key) {
	warnings := restickeys.Warnings(keys)
	if opts.json {
		printKeysJSON(stdout, opts.name, keys, warnings)
		return
	}
	w := tabwriter.NewWriter(stdout, 0, 8, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tPURPOSE\tLABEL\tHOST\tCREATED\tCURRENT")
	for _, k := range keys {
		purpose, label := keyTagColumns(k)
		current := ""
		if k.Current {
			current = "*"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", k.ID[:8], purpose, label, dashIfEmpty(k.HostName), k.Created.UTC().Format(time.RFC3339), current)
	}
	_ = w.Flush()
	for _, code := range warnings {
		_, _ = fmt.Fprintln(stderr, keyWarningText(code, opts.name))
	}
}

// keyTagColumns are PURPOSE and LABEL of a key; "-" for what Sard did not write.
func keyTagColumns(k restic.Key) (purpose, label string) {
	tag, _ := restickeys.TagOf(k.UserName)
	return dashIfEmpty(string(tag.Purpose)), dashIfEmpty(tag.Label)
}
