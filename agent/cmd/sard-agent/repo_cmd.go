// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// sard-agent repo init and repo list (A5b): docs/specs/agent/repo-init.feature.
// The decisions (which checks come in which order, what restic's answers
// mean) are agent/internal/repoinit; these files are the command line:
// repo_cmd.go (dispatch, dependencies), repo_flags.go (flags, --help),
// repo_init_run.go and repo_list_run.go (the pipelines), repo_report.go
// (messages, the exit-code tables).
package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/crypto"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

func isRepoCommand(args []string) bool {
	return len(args) > 0 && args[0] == "repo"
}

// runRepo is "sard-agent repo ...": args excludes the "repo" word itself.
// It never reads stdin.
func runRepo(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return runRepoWithDeps(ctx, args, stdout, stderr, productionHostDeps())
}

func runRepoWithDeps(ctx context.Context, args []string, stdout, stderr io.Writer, deps hostDeps) int {
	run := repoSubcommand(args)
	if run == nil {
		_, _ = fmt.Fprintln(stderr, "sard-agent repo: want a subcommand: init or list")
		return exitUsage
	}
	if hasHelpFlag(args[1:]) {
		printRepoHelp(stdout, args[0])
		return exitOK
	}
	return run(ctx, args[1:], stdout, stderr, deps)
}

// repoSubcommand is the pipeline of args[0], nil for anything but init and list.
func repoSubcommand(args []string) func(context.Context, []string, io.Writer, io.Writer, hostDeps) int {
	if len(args) > 0 {
		switch args[0] {
		case "init":
			return runRepoInit
		case "list":
			return runRepoList
		}
	}
	return nil
}

// newRestic is the wrapper for one repository of the config.
func newRestic(cfg config.Config, binary string, deps hostDeps, repo config.Repository, as *restic.RunAs) *restic.CLI {
	return restic.New(restic.Options{
		Binary:   binary,
		CacheDir: cfg.Restic.CacheDir,
		Path:     deps.pathEnv,
		Exec:     deps.exec,
		Keys:     crypto.NewResticAES(cfg.PasswordFiles()),
		ReadFile: deps.readFile,
		RunAs:    as,
	}, repo)
}

// defaultRepoTimeout bounds the whole command unless --timeout says otherwise (В7).
const defaultRepoTimeout = 2 * time.Minute
