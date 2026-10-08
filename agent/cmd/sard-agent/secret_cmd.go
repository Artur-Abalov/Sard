// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// sard-agent secret set, list and remove (A8a): docs/specs/agent/host-setup.feature.
package main

import (
	"context"
	"fmt"
	"io"
)

func isSecretCommand(args []string) bool {
	return len(args) > 0 && args[0] == "secret"
}

// runSecret is "sard-agent secret ...": args excludes the "secret" word.
func runSecret(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return runSecretWithDeps(ctx, args, stdout, stderr, productionHostDeps())
}

type hostCommand func(context.Context, []string, io.Writer, io.Writer, hostDeps) int

func runSecretWithDeps(ctx context.Context, args []string, stdout, stderr io.Writer, deps hostDeps) int {
	run := secretSubcommand(args)
	if run == nil {
		_, _ = fmt.Fprintln(stderr, "sard-agent secret: want a subcommand: set, list or remove")
		return exitUsage
	}
	if hasHelpFlag(args[1:]) {
		printSecretHelp(stdout, args[0])
		return exitOK
	}
	return run(ctx, args[1:], stdout, stderr, deps)
}

// secretSubcommand is the pipeline of args[0], nil for anything else.
func secretSubcommand(args []string) hostCommand {
	if len(args) == 0 {
		return nil
	}
	return map[string]hostCommand{"set": runSecretSet, "list": runSecretList, "remove": runSecretRemove}[args[0]]
}
