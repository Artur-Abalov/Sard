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
)

// initState is what the checks before the lock leave for the rest of the command.
type initState struct {
	cfg     config.Config
	repo    config.Repository
	checked repoinit.Checked
	binary  string
	who     hostsetup.Principal
}

// runRepoInit is "sard-agent repo init ...": args excludes "init". The
// checks run in the order of С5: flags, config, name, crypto_provider,
// password_file, env_file, restic, lock, password generation, backend.
func runRepoInit(ctx context.Context, args []string, stdout, stderr io.Writer, deps hostDeps) int {
	opts, code := parseRepoFlags("init", args, stderr, deps)
	if code != exitOK {
		return code
	}
	who, f := authorize(deps, "repo init", opts, false, true)
	if f != nil {
		return report(stderr, "repo init", f)
	}
	ctx, cancel := repoContext(ctx, deps.clock, opts.timeout)
	defer cancel(nil)
	st, code := prepareInit(ctx, opts, who, stderr, deps)
	if code != exitOK {
		return code
	}
	unlock, f := repoinit.AcquireLock(deps.openLock, cacheDir(st.cfg, deps), st.repo)
	if f != nil {
		return report(stderr, "repo init", f)
	}
	defer unlock()
	return initRepository(ctx, st, opts, stdout, stderr, deps)
}

// cacheDir is restic.cache_dir of the config, or the default when unset;
// it holds the init lock (В8а).
func cacheDir(cfg config.Config, deps hostDeps) string {
	if cfg.Restic.CacheDir != "" {
		return cfg.Restic.CacheDir
	}
	return deps.defaultCacheDir
}

// prepareInit runs the checks that come before the lock.
func prepareInit(ctx context.Context, opts hostOptions, who hostsetup.Principal, stderr io.Writer, deps hostDeps) (initState, int) {
	cfg, code := loadRepoConfig("init", opts.configPath, stderr)
	if code != exitOK {
		return initState{}, code
	}
	repo, index, f := findRepository(cfg, opts.name, opts.configPath)
	if f != nil {
		return initState{}, report(stderr, "repo init", f)
	}
	checked, f := repoinit.Preflight(deps.host(who.Service.UID), repo, index, opts.generate)
	if f != nil {
		return initState{}, report(stderr, "repo init", f)
	}
	binary, err := checkRestic(ctx, cfg, opts.configPath, deps.executable, deps.exec, runAs(who))
	if err != nil {
		return initState{}, reportRepoError(ctx, stderr, "init", err)
	}
	return initState{cfg: cfg, repo: repo, checked: checked, binary: binary, who: who}, exitOK
}

// initRepository generates the password if asked, then creates the
// repository and reports.
func initRepository(ctx context.Context, st initState, opts hostOptions, stdout, stderr io.Writer, deps hostDeps) int {
	var generated []string
	if st.checked.PasswordMissing {
		password, err := repoinit.CreatePassword(ownedWriter(deps, st.who), deps.random, st.repo)
		if err != nil {
			return reportRepoError(ctx, stderr, "init", err)
		}
		generated = []string{password}
	}
	target := repoTarget(st.repo, st.checked, generated...)
	id, f := repoinit.Create(ctx, newRestic(st.cfg, st.binary, deps, st.repo, runAs(st.who)), target)
	if f != nil {
		if generated != nil {
			_, _ = fmt.Fprintf(stderr, "sard-agent repo init: the password file %s created by this command was kept and will be used when the command is repeated\n", st.repo.PasswordFile)
		}
		return report(stderr, "repo init", f)
	}
	printInitSuccess(stdout, st.repo, id, passwordNote(opts.generate, st.checked.PasswordMissing))
	return exitOK
}

// repoTarget is what repoinit needs to know of one repository; secrets are
// values the messages must not show besides the config's own.
func repoTarget(repo config.Repository, checked repoinit.Checked, secrets ...string) repoinit.Target {
	return repoinit.Target{
		Name: repo.Name, Backend: repo.Backend(), PasswordFile: repo.PasswordFile,
		Scrub: repoinit.Scrubber(repo.URL, checked.EnvAssignments, secrets...),
	}
}

// passwordNote says where the password file came from when the operator
// asked for one to be generated.
func passwordNote(generate, created bool) string {
	switch {
	case created:
		return " (created by this command)"
	case generate:
		return " (existing password file used, no password was generated)"
	}
	return ""
}

// loadRepoConfig reads the agent config; every problem is a usage error (В6).
func loadRepoConfig(sub, path string, stderr io.Writer) (config.Config, int) {
	return loadConfigFor("repo "+sub, path, stderr)
}

// loadConfigFor is loadRepoConfig for any command.
func loadConfigFor(words, path string, stderr io.Writer) (config.Config, int) {
	cfg, err := config.Load(path)
	if err != nil {
		return config.Config{}, usageFailure(stderr, words, fmt.Sprintf("reading config %s: %v", path, err))
	}
	return cfg, exitOK
}

// findRepository looks the repository up by its exact name; the index is
// the one A1's messages use (repositories[i]).
func findRepository(cfg config.Config, name, configPath string) (config.Repository, int, *refusal.Failure) {
	for i, r := range cfg.Repositories {
		if r.Name == name {
			return r, i, nil
		}
	}
	return config.Repository{}, -1, repoinit.UnknownRepository(name, configPath, repositoryNames(cfg.Repositories))
}

func printInitSuccess(stdout io.Writer, repo config.Repository, id, passwordNote string) {
	_, _ = fmt.Fprintf(stdout, "Initialized repository %q\n", repo.Name)
	_, _ = fmt.Fprintf(stdout, "  backend:       %s\n", repo.Backend())
	_, _ = fmt.Fprintf(stdout, "  repository_id: %s\n", id)
	_, _ = fmt.Fprintf(stdout, "  password file: %s%s\n", repo.PasswordFile, passwordNote)
	_, _ = fmt.Fprintf(stdout, "\nWARNING: the key of this repository exists only on this host, in %s.\n", repo.PasswordFile)
	_, _ = fmt.Fprintln(stdout, "Save a copy of that file outside this host now: without it, if this host is lost, the backups are unrecoverable.")
	_, _ = fmt.Fprintln(stdout, "\nNext: restart the sard-agent service so that the server receives the repository_id in Register.")
}
