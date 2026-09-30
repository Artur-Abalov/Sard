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
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/crypto"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/secrets"
)

// repoDeps is everything "sard-agent repo ..." reaches outside its
// arguments; tests substitute restic, the clock and the file system.
type repoDeps struct {
	clock      clock
	exec       restic.Executor
	executable func() (string, error)
	uid        uint32
	stat       secrets.StatFunc
	readFile   func(name string) ([]byte, error)
	writeNew   func(path string, data []byte) error
	random     io.Reader
	pathEnv    string
	// defaultConfig is the config used without --config.
	defaultConfig string
}

func productionRepoDeps() repoDeps {
	return repoDeps{
		clock:      realEnrollClock{},
		exec:       restic.ProcessExecutor{},
		executable: os.Executable,
		uid:        uint32(os.Getuid()),
		stat:       secrets.RealStat,
		readFile:   os.ReadFile,
		writeNew:   repoinit.WriteNew,
		random:     rand.Reader,
		pathEnv:    os.Getenv("PATH"),

		defaultConfig: defaultEnrollConfigPath,
	}
}

func isRepoCommand(args []string) bool {
	return len(args) > 0 && args[0] == "repo"
}

// runRepo is "sard-agent repo ...": args excludes the "repo" word itself.
// It never reads stdin.
func runRepo(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return runRepoWithDeps(ctx, args, stdout, stderr, productionRepoDeps())
}

func runRepoWithDeps(ctx context.Context, args []string, stdout, stderr io.Writer, deps repoDeps) int {
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
func repoSubcommand(args []string) func(context.Context, []string, io.Writer, io.Writer, repoDeps) int {
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
func newRestic(cfg config.Config, binary string, deps repoDeps, repo config.Repository) *restic.CLI {
	return restic.New(restic.Options{
		Binary:   binary,
		CacheDir: cfg.Restic.CacheDir,
		Path:     deps.pathEnv,
		Exec:     deps.exec,
		Keys:     crypto.NewResticAES(cfg.PasswordFiles()),
		ReadFile: deps.readFile,
	}, repo)
}

// defaultRepoTimeout bounds the whole command unless --timeout says otherwise (В7).
const defaultRepoTimeout = 2 * time.Minute

func (d repoDeps) host() repoinit.Host {
	return repoinit.Host{UID: d.uid, Stat: d.stat, ReadFile: d.readFile}
}
