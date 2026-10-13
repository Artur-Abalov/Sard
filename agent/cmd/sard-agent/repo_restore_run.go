// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strconv"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/restore"
)

// restoreRun is what repo restore carries from step to step.
type restoreRun struct {
	opts           hostOptions
	deps           hostDeps
	stdout, stderr io.Writer
	st             snapshotsState
	// target is the clean path of the target, checked before the storage is touched.
	target string
}

// runRepoRestore is "sard-agent repo restore <name> <snapshot> --target
// <dir>": args excludes "restore". The checks come in the order of Р69:
// flags, rights, name, config, repository, the target as it is, the service
// user, restic, the files of the repository, the list, the snapshot, the
// snapshot's kind and --path, the target made and locked, the restore, the
// audit line.
func runRepoRestore(ctx context.Context, args []string, stdout, stderr io.Writer, deps hostDeps) int {
	opts, code := parseRepoFlags("restore", args, stderr, deps)
	if code != exitOK {
		return code
	}
	_, f := authorize(deps, "repo restore", opts, true, false)
	if f != nil {
		return report(stderr, "repo restore", f)
	}
	ctx, cancel := repoContext(ctx, deps.clock, opts.timeout)
	defer cancel(nil)
	run := &restoreRun{opts: opts, deps: deps, stdout: stdout, stderr: stderr}
	st, code := prepareRepository(ctx, "restore", opts, stderr, deps, run.checkHost)
	if code != exitOK {
		return code
	}
	run.st = st
	return run.restore(ctx)
}

// checkHost is the checks of the host before the storage: the target as it
// is, then the service user, who restic runs as.
func (r *restoreRun) checkHost(cfg config.Config, _ config.Repository) (hostsetup.Principal, int) {
	target, f := restore.CheckTarget(r.deps.fs, restore.Target{
		Path: r.opts.target, Overwrite: r.opts.overwrite, AllowRoot: r.opts.allowRoot, Avoid: avoidedDirs(cfg, r.deps),
	})
	if f != nil {
		return hostsetup.Principal{}, report(r.stderr, "repo restore", f)
	}
	r.target = target
	who, f := authorize(r.deps, "repo restore", r.opts, true, true)
	if f != nil {
		return hostsetup.Principal{}, report(r.stderr, "repo restore", f)
	}
	return who, exitOK
}

// avoidedDirs are the directories a restore must not go into: the local
// repositories and the cache of restic (Р59).
func avoidedDirs(cfg config.Config, deps hostDeps) []string {
	dirs := []string{filepath.Clean(cacheDir(cfg, deps))}
	for _, repo := range cfg.Repositories {
		if repo.Backend() == "local" {
			dirs = append(dirs, filepath.Clean(repo.URL))
		}
	}
	return dirs
}

// restore is the part of the command that reads the storage.
func (r *restoreRun) restore(ctx context.Context) int {
	st, who := r.st, r.st.who
	list, f := readSnapshots(ctx, st, r.opts, who, r.deps)
	if f != nil {
		return report(r.stderr, "repo restore", f)
	}
	snap, f := restore.Select(list, r.opts.address, st.repo.Name, r.opts.source)
	if f != nil {
		return report(r.stderr, "repo restore", f)
	}
	target := targetOf(st, who, r.deps)
	reader := newRestic(st.cfg, st.binary, r.deps, st.repo, runAs(who))
	if f := restore.RequirePaths(ctx, reader, snap, r.opts.paths, target); f != nil {
		return report(r.stderr, "repo restore", f)
	}
	req, f := r.request(snap)
	if f != nil {
		return report(r.stderr, "repo restore", f)
	}
	return r.write(ctx, req)
}

// request is what to restore and how restic reaches the storage.
func (r *restoreRun) request(snap restore.Snapshot) (restore.Request, *refusal.Failure) {
	req := restore.Request{Snapshot: snap, Target: r.target, Overwrite: r.opts.overwrite, Paths: r.opts.paths}
	command, f := r.sftpCommand()
	req.SFTPCommand = command
	return req, f
}

// write makes the target, restores into it and records the outcome.
func (r *restoreRun) write(ctx context.Context, req restore.Request) int {
	st, snap := r.st, req.Snapshot
	held, f := restore.OpenTarget(r.deps.fs, r.target)
	if f != nil {
		return report(r.stderr, "repo restore", f)
	}
	// restic runs as root so that the files get the owners of the snapshot;
	// the env_file is still the file of the service user (Р63).
	writer := newResticReading(st.cfg, st.binary, r.deps, st.repo, nil, st.who.Service.UID)
	summary, f := restore.Run(ctx, writer, req, targetOf(st, st.who, r.deps))
	if f != nil {
		held.Abandon()
		r.record(snap, fmt.Sprintf("restore to %s failed: %s", auditPath(r.target), f.Reason))
		return report(r.stderr, "repo restore", f)
	}
	held.Release()
	r.record(snap, "restored to "+auditPath(r.target))
	printRestored(r.stdout, st.repo.Name, snap, r.target, r.opts.paths, summary)
	return exitOK
}

// sftpCommand is how restic starts ssh as the service user for an sftp:
// repository (Р63); any other repository needs nothing.
func (r *restoreRun) sftpCommand() (string, *refusal.Failure) {
	if r.st.repo.Backend() != "sftp" {
		return "", nil
	}
	address, f := hostsetup.CheckSFTPAddress(r.st.repo.URL)
	if f != nil {
		return "", f
	}
	service := r.st.who.Service
	return restore.SFTPCommand(restic.RunAs{UID: service.UID, GID: service.GID}, address), nil
}

// record writes the audit line of a restore whose writing began (Р65).
func (r *restoreRun) record(snap restore.Snapshot, outcome string) {
	c := newHostCmd("repo restore", r.opts, r.st.who, r.stdout, r.stderr, r.deps)
	c.record("repository", r.st.repo.Name, "snapshot "+snap.ShortID+" "+outcome)
}

// auditPath is the path as it is, unless it holds anything but plain
// characters: then it is quoted, so that a line break cannot make a second
// line of the log.
func auditPath(path string) string {
	if quoted := strconv.Quote(path); quoted != `"`+path+`"` {
		return quoted
	}
	return path
}

func printRestored(stdout io.Writer, repoName string, snap restore.Snapshot, target string, paths []string, summary restic.RestoreSummary) {
	_, _ = fmt.Fprintf(stdout, "repository %s: snapshot %s restored\n", repoName, snap.ID)
	_, _ = fmt.Fprintf(stdout, "  time:     %s\n  source:   %s\n  run:      %s\n  target:   %s\n", snap.Stamp(), dashIfNil(snap.Source), dashIfNil(snap.Run), target)
	if len(paths) > 0 {
		_, _ = fmt.Fprintf(stdout, "  paths:    %v\n", paths)
	}
	_, _ = fmt.Fprintf(stdout, "  restored: %d files, %d bytes\n", summary.Files, summary.Bytes)
	example := append(append([]string{}, paths...), snap.Paths...)
	if len(example) > 0 {
		_, _ = fmt.Fprintf(stdout, "The files lie in %s under their original paths, for example %s.\n", target, filepath.Join(target, example[0]))
	}
}
