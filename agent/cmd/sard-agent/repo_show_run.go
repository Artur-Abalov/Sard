// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"fmt"
	"io"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
)

// runRepoShow is "sard-agent repo show <name>": args excludes "show". One
// restic cat config; the exit code is the class of the status, as for a
// row of repo list (Л8).
func runRepoShow(ctx context.Context, args []string, stdout, stderr io.Writer, deps hostDeps) int {
	opts, code := parseRepoFlags("show", args, stderr, deps)
	if code != exitOK {
		return code
	}
	who, f := authorize(deps, "repo show", opts, false, true)
	if f != nil {
		return reportRepoFailure(stderr, "show", f)
	}
	ctx, cancel := repoContext(ctx, deps.clock, opts.timeout)
	defer cancel(nil)
	cfg, code := loadRepoConfig("show", opts.configPath, stderr)
	if code != exitOK {
		return code
	}
	repo, index, f := findRepository(cfg, opts.name, opts.configPath)
	if f != nil {
		return reportRepoFailure(stderr, "show", f)
	}
	binary, err := checkRestic(ctx, cfg, opts.configPath, deps.executable, deps.exec, runAs(who))
	if err != nil {
		return reportRepoError(ctx, stderr, "show", err)
	}
	row := inspectRepository(ctx, cfg, binary, index, who, deps)
	if f := repoinit.Interruption(ctx); f != nil && f.Reason == repoinit.Interrupted {
		return reportRepoFailure(stderr, "show", f)
	}
	printCard(stdout, repo, row, opts.json)
	if row.problem != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %s\n", row.name, row.problem)
	}
	return listExitCode([]listRow{row})
}

// dashIfEmpty is the text of a field that has no value.
func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func printCard(stdout io.Writer, repo config.Repository, row listRow, asJSON bool) {
	if asJSON {
		printCardJSON(stdout, repo, row)
		return
	}
	for _, f := range []struct{ key, value string }{
		{"name", repo.Name}, {"backend", repo.Backend()}, {"address", config.RedactURL(repo.URL)},
		{"status", row.status()}, {"repository_id", row.repositoryID()},
		{"password_file", repo.PasswordFile}, {"env_file", dashIfEmpty(repo.EnvFile)}, {"defined_in", row.definedIn},
	} {
		_, _ = fmt.Fprintf(stdout, "%-14s %s\n", f.key+":", f.value)
	}
}

func printCardJSON(stdout io.Writer, repo config.Repository, row listRow) {
	printJSON(stdout, struct {
		Name         string  `json:"name"`
		Backend      string  `json:"backend"`
		Address      string  `json:"address"`
		Status       string  `json:"status"`
		RepositoryID *string `json:"repository_id"`
		PasswordFile string  `json:"password_file"`
		EnvFile      *string `json:"env_file"`
		DefinedIn    string  `json:"defined_in"`
	}{
		repo.Name, repo.Backend(), config.RedactURL(repo.URL), row.status(), nullIfDash(row.repositoryID()),
		repo.PasswordFile, nullIfDash(repo.EnvFile), row.definedIn,
	})
}
