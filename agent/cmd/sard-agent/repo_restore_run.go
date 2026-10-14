// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"encoding/hex"
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
	// held is the target: judged before the storage is touched, held and
	// locked from then on (Р79).
	held *restore.Held
}

// target is the clean path of the target.
func (r *restoreRun) target() string { return r.held.Path }

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
	defer run.release()
	st, code := prepareRepository(ctx, "restore", opts, stderr, deps, run.checkHost, run.checkTools)
	if code != exitOK {
		return code
	}
	run.st = st
	return run.restore(ctx)
}

// checkHost is the checks of the host before the storage: the target as it
// is, then the service user, who restic runs as.
func (r *restoreRun) checkHost(cfg config.Config, repo config.Repository) (hostsetup.Principal, int) {
	if err := restic.CheckRootBackend(repo); err != nil {
		return hostsetup.Principal{}, report(r.stderr, "repo restore", restore.InputsFailure(err))
	}
	held, f := restore.CheckTarget(r.deps.fs, restore.Target{
		Path: r.opts.target, AllowRoot: r.opts.allowRoot, Avoid: avoidedDirs(cfg, r.deps),
	})
	if f != nil {
		return hostsetup.Principal{}, report(r.stderr, "repo restore", f)
	}
	r.held = held
	who, f := authorize(r.deps, "repo restore", r.opts, true, true)
	if f != nil {
		return hostsetup.Principal{}, report(r.stderr, "repo restore", f)
	}
	return who, exitOK
}

// release lets go of the target, whatever way the command ends.
func (r *restoreRun) release() {
	if r.held != nil {
		r.held.Release()
	}
}

// checkTools is what restic needs next to itself, before the files of the
// repository are judged (Р69): setpriv for an sftp: repository (Р77), then
// the password file and the env_file as root will get them, read here once,
// before anything is listed or made (Р78, Р72).
func (r *restoreRun) checkTools(st snapshotsState) *refusal.Failure {
	c := newHostCmd("repo restore", r.opts, st.who, r.stdout, r.stderr, r.deps)
	if f := restore.CheckSetpriv(st.repo.Backend(), func(program string) bool {
		_, found := c.findProgram(program)
		return found
	}); f != nil {
		return f
	}
	if err := newResticAsRoot(st.cfg, st.binary, r.deps, st.repo, st.who.Service.UID).CheckRootInputs(context.Background()); err != nil {
		return named(restore.InputsFailure(err))
	}
	return nil
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
	if restore.KindOf(snap) != restore.KindDirectory {
		return r.restorePostgres(ctx, reader, list, snap)
	}
	if f := restore.RequirePaths(ctx, reader, snap, r.opts.paths, target); f != nil {
		return report(r.stderr, "repo restore", f)
	}
	req, f := r.request(snap)
	if f != nil {
		return report(r.stderr, "repo restore", f)
	}
	return r.write(ctx, req)
}

// restorePostgres lays out a snapshot of PostgreSQL as files and tells the
// commands that restore it (Р62). The content comes from restic dump, run as
// the service user like the list; root writes the files itself.
func (r *restoreRun) restorePostgres(ctx context.Context, reader *restic.CLI, list []restore.Snapshot, snap restore.Snapshot) int {
	if len(r.opts.paths) > 0 {
		return report(r.stderr, "repo restore", &refusal.Failure{Class: refusal.ClassUsage,
			Detail: "--path does not apply to a PostgreSQL snapshot: it holds one file and is laid out whole"})
	}
	plan, f := restore.PlanPostgres(list, snap)
	if f != nil {
		return report(r.stderr, "repo restore", f)
	}
	if f := r.held.Create(); f != nil {
		return report(r.stderr, "repo restore", f)
	}
	if f := restore.WritePostgres(ctx, reader, r.held, plan, r.randomName, targetOf(r.st, r.st.who, r.deps)); f != nil {
		r.held.Abandon()
		r.record(snap, fmt.Sprintf("restore to %s failed: %s", auditPath(r.target()), f.Reason))
		return report(r.stderr, "repo restore", f)
	}
	r.held.Release()
	r.record(snap, "restored to "+auditPath(r.target()))
	printPostgres(r.stdout, r.st.repo.Name, snap, r.target(), plan)
	return exitOK
}

// request is what to restore and how restic reaches the storage.
func (r *restoreRun) request(snap restore.Snapshot) (restore.Request, *refusal.Failure) {
	req := restore.Request{Snapshot: snap, Target: r.target(), Paths: r.opts.paths}
	command, f := r.sftpCommand()
	req.SFTPCommand = command
	return req, f
}

// write makes the target, the temporary directory in it, restores and
// records the outcome.
func (r *restoreRun) write(ctx context.Context, req restore.Request) int {
	st, snap, held := r.st, req.Snapshot, r.held
	if f := held.Create(); f != nil {
		return report(r.stderr, "repo restore", f)
	}
	stage, f := held.Stage(r.randomName)
	if f != nil {
		held.Abandon()
		return report(r.stderr, "repo restore", f)
	}
	req.Staging = stage
	// restic runs as root so that the files get the owners of the snapshot;
	// the env_file and the password file are still files of the service user (Р63).
	writer := newResticAsRoot(st.cfg, st.binary, r.deps, st.repo, st.who.Service.UID)
	summary, f := restore.Run(ctx, writer, req, targetOf(st, st.who, r.deps))
	if f != nil {
		held.Abandon()
		r.record(snap, fmt.Sprintf("restore to %s failed: %s", auditPath(r.target()), f.Reason))
		return report(r.stderr, "repo restore", f)
	}
	held.Release()
	r.record(snap, "restored to "+auditPath(r.target()))
	printRestored(r.stdout, st.repo.Name, snap, r.target(), r.opts.paths, summary)
	return exitOK
}

// randomName is the random part of the name of the temporary directory.
func (r *restoreRun) randomName() string {
	var b [8]byte
	_, _ = io.ReadFull(r.deps.random, b[:]) // a short read leaves zeros: a taken name is replaced
	return hex.EncodeToString(b[:])
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

func printPostgres(stdout io.Writer, repoName string, snap restore.Snapshot, target string, plan restore.PGPlan) {
	_, _ = fmt.Fprintf(stdout, "repository %s: snapshot %s restored\n", repoName, snap.ID)
	_, _ = fmt.Fprintf(stdout, "  time:     %s\n  source:   %s\n  run:      %s\n  target:   %s\n", snap.Stamp(), dashIfNil(snap.Source), dashIfNil(snap.Run), target)
	for _, file := range plan.Files {
		_, _ = fmt.Fprintf(stdout, "  file:     %s\n", filepath.Join(target, file.Name))
	}
	for _, line := range plan.Hints(target) {
		_, _ = fmt.Fprintln(stdout, line)
	}
}
