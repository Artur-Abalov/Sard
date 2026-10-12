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
	printSnapshots(stdout, st.repo.Name, opts, list)
	return exitOK
}

func prepareSnapshots(ctx context.Context, opts hostOptions, who hostsetup.Principal, stderr io.Writer, deps hostDeps) (snapshotsState, int) {
	cfg, code := loadRepoConfig("snapshots", opts.configPath, stderr)
	if code != exitOK {
		return snapshotsState{}, code
	}
	repo, index, f := findRepository(cfg, opts.name, opts.configPath)
	if f != nil {
		return snapshotsState{}, report(stderr, "repo snapshots", f)
	}
	binary, err := checkRestic(ctx, cfg, opts.configPath, deps.executable, deps.exec, runAs(who))
	if err != nil {
		return snapshotsState{}, reportRepoError(ctx, stderr, "snapshots", err)
	}
	checked, f := repoinit.Preflight(deps.host(who.Service.UID), repo, index, false)
	if f != nil {
		return snapshotsState{}, report(stderr, "repo snapshots", named(f))
	}
	return snapshotsState{cfg: cfg, repo: repo, checked: checked, binary: binary}, exitOK
}

// readSnapshots is the one restic snapshots call; the first access to the
// storage is limited by --connect-timeout (Р54).
func readSnapshots(ctx context.Context, st snapshotsState, opts hostOptions, who hostsetup.Principal, deps hostDeps) ([]restore.Snapshot, *refusal.Failure) {
	var log repoconnect.Log
	cli := newRestic(st.cfg, st.binary, deps, st.repo, runAs(who)).WithStderr(&log)
	target := snapshotsTarget(st, passwordValues(st.repo, who, deps))
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

// snapshotsTarget is what the messages need to know of the repository; a
// remote one is told apart by the answer of its storage (Р34).
func snapshotsTarget(st snapshotsState, secrets []string) repoinit.Target {
	target := repoTarget(st.repo, st.checked, secrets...)
	target.Where = config.RedactURL(st.repo.URL)
	if st.repo.Backend() == "s3" {
		markS3(&target, st.repo.URL)
	}
	if st.repo.Backend() == "sftp" {
		markSFTP(&target, st.repo.URL)
	}
	return target
}

func markS3(target *repoinit.Target, url string) {
	if a, f := hostsetup.CheckS3Address(url); f == nil {
		target.Remote, target.Bucket = true, a.Bucket
	}
}

func markSFTP(target *repoinit.Target, url string) {
	if a, f := hostsetup.CheckSFTPAddress(url); f == nil {
		target.Remote, target.Directory = true, a.Path
	}
}

func printSnapshots(stdout io.Writer, repoName string, opts hostOptions, list []restore.Snapshot) {
	switch {
	case opts.json:
		printSnapshotsJSON(stdout, repoName, list)
	case len(list) == 0:
		_, _ = fmt.Fprintln(stdout, emptyListLine(repoName, opts))
	default:
		printSnapshotsTable(stdout, list)
	}
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
	b.WriteString("ID TIME HOST SOURCE RUN PATHS\n")
	for _, s := range list {
		fmt.Fprintf(&b, "%s %s %s %s %s %s\n", s.ShortID, s.Stamp(), s.Hostname, dashIfNil(s.Source), dashIfNil(s.Run), strings.Join(s.Paths, ","))
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
			Source: s.Source, Run: s.Run, Step: s.Step, Tags: nonNil(s.Tags),
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
