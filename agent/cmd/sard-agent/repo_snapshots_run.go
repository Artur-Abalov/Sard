// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoconnect"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restore"
)

// snapshotsState is what the checks before the storage leave for the list.
type snapshotsState struct {
	cfg     config.Config
	repo    config.Repository
	checked repoinit.Checked
	binary  string
	// who is who runs the command, with the service user known.
	who hostsetup.Principal
}

// runRepoSnapshots is "sard-agent repo snapshots <name>": args excludes
// "snapshots". The checks come in the order of Р69: flags, rights, config,
// name, restic, the files of the repository, the list.
func runRepoSnapshots(ctx context.Context, args []string, stdout, stderr io.Writer, deps hostDeps) int {
	opts, code := parseRepoFlags("snapshots", args, stderr, deps)
	if code != exitOK {
		return code
	}
	who, f := authorize(deps, "repo snapshots", opts, false, true)
	if f != nil {
		return report(stderr, "repo snapshots", f)
	}
	ctx, cancel := repoContext(ctx, deps.clock, opts.timeout)
	defer cancel(nil)
	st, code := prepareSnapshots(ctx, opts, who, stderr, deps)
	if code != exitOK {
		return code
	}
	list, f := readSnapshots(ctx, st, opts, who, deps)
	if f != nil {
		return report(stderr, "repo snapshots", f)
	}
	printSnapshots(stdout, stderr, st.repo.Name, opts, list)
	return exitOK
}

func prepareSnapshots(ctx context.Context, opts hostOptions, who hostsetup.Principal, stderr io.Writer, deps hostDeps) (snapshotsState, int) {
	return prepareRepository(ctx, "snapshots", opts, stderr, deps, func(config.Config, config.Repository) (hostsetup.Principal, int) { return who, exitOK }, noTools)
}

// noTools: repo snapshots needs nothing next to restic.
func noTools(snapshotsState) *refusal.Failure { return nil }

// prepareRepository is what repo snapshots and repo restore do before the
// storage is touched: the config, the name, then check (the command's own
// checks of the host, Р69, which also tell who runs restic), restic, tools
// (what the command needs next to restic, if anything: setpriv, the
// password and the env_file for root; Р77, Р78, Р72) and the files of
// the repository.
func prepareRepository(ctx context.Context, sub string, opts hostOptions, stderr io.Writer, deps hostDeps, check func(config.Config, config.Repository) (hostsetup.Principal, int), tools func(snapshotsState) *refusal.Failure) (snapshotsState, int) {
	cfg, repo, index, code := findConfigured(sub, opts, stderr)
	if code != exitOK {
		return snapshotsState{}, code
	}
	who, code := check(cfg, repo)
	if code != exitOK {
		return snapshotsState{}, code
	}
	binary, err := checkRestic(ctx, cfg, opts.configPath, deps.executable, deps.exec, runAs(who))
	if err != nil {
		return snapshotsState{}, reportRepoError(ctx, stderr, sub, err)
	}
	if f := tools(snapshotsState{cfg: cfg, repo: repo, binary: binary, who: who}); f != nil {
		return snapshotsState{}, report(stderr, "repo "+sub, f)
	}
	checked, f := repoinit.Preflight(deps.host(who.Service.UID), repo, index, false)
	if f != nil {
		return snapshotsState{}, report(stderr, "repo "+sub, named(f))
	}
	return snapshotsState{cfg: cfg, repo: repo, checked: checked, binary: binary, who: who}, exitOK
}

// findConfigured reads the config and looks the repository up by its name;
// the index is the one A1's messages use.
func findConfigured(sub string, opts hostOptions, stderr io.Writer) (config.Config, config.Repository, int, int) {
	cfg, code := loadRepoConfig(sub, opts.configPath, stderr)
	if code != exitOK {
		return config.Config{}, config.Repository{}, -1, code
	}
	repo, index, f := findRepository(cfg, opts.name, opts.configPath)
	if f != nil {
		return config.Config{}, config.Repository{}, -1, report(stderr, "repo "+sub, f)
	}
	return cfg, repo, index, exitOK
}

// readSnapshots is the one restic snapshots call; the first access to the
// storage is limited by --connect-timeout (Р54).
func readSnapshots(ctx context.Context, st snapshotsState, opts hostOptions, who hostsetup.Principal, deps hostDeps) ([]restore.Snapshot, *refusal.Failure) {
	var log repoconnect.Log
	cli := newRestic(st.cfg, st.binary, deps, st.repo, runAs(who)).WithStderr(&log)
	target := targetOf(st, who, deps)
	var list []restore.Snapshot
	bound := repoconnect.Bound{Clock: deps.clock, Timeout: opts.connectTimeout}
	f := bound.Attempt(ctx, target, &log,
		func(limited context.Context) (err error) {
			list, err = restore.List(limited, cli, opts.source)
			return err
		},
		func(ctx context.Context, err error) *refusal.Failure { return restore.ListFailure(ctx, err, target) })
	return list, f
}

// targetOf is the repository as the messages of a failure describe it.
func targetOf(st snapshotsState, who hostsetup.Principal, deps hostDeps) repoinit.Target {
	return repoconnect.TargetFor(st.repo, st.checked.EnvAssignments, passwordValues(st.repo, who, deps)...)
}

// named puts the reason in front of the text of A1 that stands alone at
// the start of the service: this command says SECRET_FILE_REJECTED (Р53).
func named(f *refusal.Failure) *refusal.Failure {
	if f.Reason == refusal.SecretFileRejected {
		return &refusal.Failure{Reason: f.Reason, Class: f.Class, Detail: string(f.Reason) + ": " + f.Detail}
	}
	return f
}

// passwordValues are the password of the repository as restic reads it from
// the file (without the white space around it): restic's text must not show
// it (Р67). The file passed the owner check; one that cannot be read now
// gives nothing to hide.
func passwordValues(repo config.Repository, who hostsetup.Principal, deps hostDeps) []string {
	data, err := deps.readOwned(repo.PasswordFile, who.Service.UID)
	if err != nil {
		return nil
	}
	return []string{string(data), strings.TrimSpace(string(data))}
}

func printSnapshots(stdout, stderr io.Writer, repoName string, opts hostOptions, list []restore.Snapshot) {
	switch {
	case opts.json:
		printSnapshotsJSON(stdout, repoName, list)
	case len(list) == 0:
		_, _ = fmt.Fprintln(stdout, emptyListLine(repoName, opts))
	default:
		printSnapshotsTable(stdout, list)
		explainUnconfirmed(stderr, list)
	}
}

// explainUnconfirmed is Р90: one line on stderr when the completeness of
// some snapshots is not confirmed.
func explainUnconfirmed(stderr io.Writer, list []restore.Snapshot) {
	n := 0
	for _, s := range list {
		if s.Completeness == restore.Unconfirmed {
			n++
		}
	}
	if n > 0 {
		_, _ = fmt.Fprintf(stderr, "completeness of %d snapshot(s) is not confirmed: the backup that made them did not confirm that every file was read (see sard-agent repo snapshots --help)\n", n)
	}
}

// completeColumn is the COMPLETE column: "-" when it does not apply.
func completeColumn(s restore.Snapshot) string {
	if s.Completeness == restore.NotApplicable {
		return "-"
	}
	return string(s.Completeness)
}

func emptyListLine(repoName string, opts hostOptions) string {
	if opts.source != "" {
		return fmt.Sprintf("no snapshots of source %s in repository %s", opts.source, repoName)
	}
	return fmt.Sprintf("no snapshots in repository %s", repoName)
}

// printSnapshotsTable is Р55: a header and a line per snapshot, the
// columns separated by one space.
func printSnapshotsTable(stdout io.Writer, list []restore.Snapshot) {
	var b strings.Builder
	b.WriteString("ID TIME HOST SOURCE RUN COMPLETE PATHS\n")
	for _, s := range list {
		fmt.Fprintf(&b, "%s %s %s %s %s %s %s\n", s.ShortID, s.Stamp(), s.Hostname, dashIfNil(s.Source), dashIfNil(s.Run), completeColumn(s), strings.Join(s.Paths, ","))
	}
	_, _ = io.WriteString(stdout, b.String())
}

func dashIfNil(s *string) string {
	if s == nil {
		return "-"
	}
	return *s
}

type snapshotJSON struct {
	ID       string   `json:"id"`
	ShortID  string   `json:"short_id"`
	Time     string   `json:"time"`
	Hostname string   `json:"hostname"`
	Paths    []string `json:"paths"`
	Source   *string  `json:"source"`
	Run      *string  `json:"run"`
	Step     *string  `json:"step"`
	Tags     []string `json:"tags"`
	// Completeness is "confirmed", "unconfirmed" or null (Р91).
	Completeness *string `json:"completeness"`
}

// printSnapshotsJSON is Р56: {"repository":"<name>","snapshots":[...]}.
func printSnapshotsJSON(stdout io.Writer, repoName string, list []restore.Snapshot) {
	out := struct {
		Repository string         `json:"repository"`
		Snapshots  []snapshotJSON `json:"snapshots"`
	}{Repository: repoName, Snapshots: make([]snapshotJSON, 0, len(list))}
	for _, s := range list {
		out.Snapshots = append(out.Snapshots, snapshotJSON{
			ID: s.ID, ShortID: s.ShortID, Time: s.Stamp(), Hostname: s.Hostname, Paths: nonNil(s.Paths),
			Source: s.Source, Run: s.Run, Step: s.Step, Tags: nonNil(s.Tags), Completeness: completenessJSON(s),
		})
	}
	printJSON(stdout, out)
}

// nonNil is the slice, or an empty one: JSON shows [] and not null.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func completenessJSON(s restore.Snapshot) *string {
	if s.Completeness == restore.NotApplicable {
		return nil
	}
	c := string(s.Completeness)
	return &c
}
