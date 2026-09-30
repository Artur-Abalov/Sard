// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
)

// listRow is one repository of "repo list": name, backend, and either its
// state or the problem that kept the check from telling it.
type listRow struct {
	name, backend string
	id            string
	initialized   bool
	problem       *repoinit.Failure
}

// status is the STATUS column (Л3).
func (r listRow) status() string {
	switch {
	case r.problem != nil:
		return string(r.problem.Reason)
	case r.initialized:
		return "initialized"
	}
	return "not-initialized"
}

func (r listRow) repositoryID() string {
	if r.problem != nil || !r.initialized {
		return "-"
	}
	return r.id
}

// runRepoList is "sard-agent repo list ...": args excludes "list".
func runRepoList(ctx context.Context, args []string, stdout, stderr io.Writer, deps repoDeps) int {
	opts, code := parseRepoFlags("list", args, stderr, deps)
	if code != exitOK {
		return code
	}
	ctx, cancel := repoContext(ctx, deps.clock, opts.timeout)
	defer cancel(nil)
	cfg, code := loadRepoConfig("list", opts.configPath, stderr)
	if code != exitOK {
		return code
	}
	binary, err := checkRestic(ctx, cfg, opts.configPath, deps.executable, deps.exec)
	if err != nil {
		return reportRepoError(ctx, stderr, "list", err)
	}
	if len(cfg.Repositories) == 0 {
		_, _ = fmt.Fprintf(stdout, "no repositories configured in %s\n", opts.configPath)
		return exitOK
	}
	rows := make([]listRow, len(cfg.Repositories))
	for i := range cfg.Repositories {
		rows[i] = inspectRepository(ctx, cfg, binary, i, deps)
	}
	return reportList(ctx, rows, stdout, stderr)
}

// reportList prints the table and one message per problem row; an
// interrupt prints no table (Л9).
func reportList(ctx context.Context, rows []listRow, stdout, stderr io.Writer) int {
	if f := repoinit.Interruption(ctx); f != nil && f.Reason == repoinit.Interrupted {
		return reportRepoFailure(stderr, "list", f)
	}
	printListTable(stdout, rows)
	for _, r := range rows {
		if r.problem != nil {
			_, _ = fmt.Fprintf(stderr, "%s: %s\n", r.name, r.problem)
		}
	}
	return listExitCode(rows)
}

// inspectRepository is one row (Л6): the checks of repo init before the
// backend, then restic cat config. Once ctx has ended nothing more runs.
func inspectRepository(ctx context.Context, cfg config.Config, binary string, index int, deps repoDeps) listRow {
	repo := cfg.Repositories[index]
	row := listRow{name: repo.Name, backend: repo.Backend()}
	checked, f := repoinit.Preflight(deps.host(), repo, index, false)
	if f == nil {
		f = repoinit.Interruption(ctx)
	}
	if f != nil {
		row.problem = f
		return row
	}
	target := repoinit.Target{
		Name: repo.Name, Backend: repo.Backend(), PasswordFile: repo.PasswordFile,
		Scrub: repoinit.Scrubber(repo.URL, checked.EnvAssignments),
	}
	row.id, row.initialized, row.problem = repoinit.Inspect(ctx, newRestic(cfg, binary, deps, repo), target)
	return row
}

func printListTable(stdout io.Writer, rows []listRow) {
	w := tabwriter.NewWriter(stdout, 0, 8, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tBACKEND\tSTATUS\tREPOSITORY_ID")
	for _, r := range rows {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.name, r.backend, r.status(), r.repositoryID())
	}
	_ = w.Flush()
}

// listExitCode is Л8: the class of the first problem row that is not
// temporary; 6 if every problem is temporary; 0 without problems.
func listExitCode(rows []listRow) int {
	code := exitOK
	for _, r := range rows {
		switch {
		case r.problem == nil:
		case r.problem.Class != repoinit.ClassTemporary:
			return repoClassCodes[r.problem.Class]
		default:
			code = exitTemporary
		}
	}
	return code
}
