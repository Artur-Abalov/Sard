// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package consolerestore restores a snapshot into a new directory of the
// host on the command of the console (R2a): all or nothing. restic, run as
// the service user, writes into a temporary directory of the agent inside
// the restore directory of the host, and one rename puts the result in the
// destination; any failure leaves no trace.
package consolerestore

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/restore"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// Repository is what the restore needs of a restic repository.
type Repository interface {
	Snapshots(ctx context.Context, tags []string) ([]restic.Snapshot, error)
	HasPath(ctx context.Context, snapshotID, path string) (bool, error)
	RestoreTo(ctx context.Context, req restic.RestoreRequest) (restic.RestoreSummary, error)
}

// Reporter is where the restore reports its progress and log lines.
type Reporter interface {
	Progress(phase agentv1.StepPhase, bytesProcessed, bytesTotal uint64)
	ProgressFiles(phase agentv1.StepPhase, bytesProcessed, bytesTotal, filesProcessed, filesTotal uint64)
	Log(level agentv1.LogLevel, text string)
}

// Options configure an Engine.
type Options struct {
	// Dir is restore.dir of the host.
	Dir string
	FS  hostsetup.FS
	// Owner is the service user and the mode of the directories the engine makes.
	Owner  hostsetup.Attrs
	Logger *slog.Logger
	// Target describes a repository by its name for the messages of a failure
	// (secrets hidden); nil: just the name, nothing to hide.
	Target func(repository string) repoinit.Target
}

// targetOf is the repository as the messages of a failure describe it.
func (e *Engine) targetOf(name string) repoinit.Target {
	if e.opts.Target != nil {
		return e.opts.Target(name)
	}
	return repoinit.Target{Name: name, Scrub: func(s string) string { return s }}
}

// Engine restores snapshots into Dir.
type Engine struct {
	opts Options

	mu   sync.Mutex
	busy map[string]bool // destinations being restored
}

// New returns an Engine.
func New(o Options) *Engine { return &Engine{opts: o, busy: map[string]bool{}} }

// claim marks the destination as being restored; false if it, a directory
// above it or one below it already is.
func (e *Engine) claim(destination string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for busy := range e.busy {
		if within(busy, destination) || within(destination, busy) {
			return false
		}
	}
	e.busy[destination] = true
	return true
}

// within: path is dir or below it (both clean and relative).
func within(dir, path string) bool {
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

func (e *Engine) free(destination string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.busy, destination)
}

// SourceTag is the tag of a snapshot that names the source it was made of
// (the sard.source tag of the step).
const SourceTag = "sard.source"

// Request is a restore of a snapshot of a source.
type Request struct {
	CommandID  string
	Repository string
	SnapshotID string
	// Source is the sard.source of the step: the snapshot must carry it.
	Source string
	Plan   sdk.RestorePlan
}

// Output is what a restore produced.
type Output struct {
	Target       string
	Files, Bytes uint64
}

// CleanStaging removes everything in .sard-staging: a restore that was
// killed left it, and no step runs yet (ВК14). It logs each entry it
// removes; what it cannot remove is returned (and logged as an error).
func (e *Engine) CleanStaging() []Leftover {
	path := filepath.Join(e.opts.Dir, stagingName)
	names, staging, err := e.stagingEntries()
	if err != nil {
		return e.unusableStaging(path, err)
	}
	defer func() { _ = staging.Close() }()
	var left []Leftover
	for _, name := range names {
		entry := filepath.Join(path, name)
		if err := hostsetup.RemoveTree(staging, name); err != nil {
			e.opts.Logger.Error("cannot remove the temporary directory of an interrupted restore; remove it by hand", "path", entry, "error", err)
			left = append(left, Leftover{Name: name, Path: entry, Err: err})
			continue
		}
		e.opts.Logger.Info("removed the temporary directory of an interrupted restore", "path", entry)
	}
	return left
}

// stagingEntries opens .sard-staging without following a link and lists it;
// the caller closes the directory.
func (e *Engine) stagingEntries() ([]string, hostsetup.Dir, error) {
	root, _, err := hostsetup.OpenDir(e.opts.FS, e.opts.Dir, nil)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = root.Close() }()
	staging, err := root.Open(stagingName)
	if err != nil {
		return nil, nil, err
	}
	names, err := staging.Names()
	if err != nil {
		_ = staging.Close()
		return nil, nil, err
	}
	slices.Sort(names)
	return names, staging, nil
}

// unusableStaging: no restore directory or no .sard-staging is nothing to
// clean; anything else (a link, a file) is left alone and reported.
func (e *Engine) unusableStaging(path string, err error) []Leftover {
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	e.opts.Logger.Error("cannot clean the temporary directories of interrupted restores", "path", path, "error", err)
	return []Leftover{{Path: path, Err: err}}
}

// Leftover is a temporary directory of an interrupted restore that could not
// be removed at start.
type Leftover struct {
	// Name is the entry, the command_id of the restore.
	Name string
	Path string
	Err  error
}

// stagingName is the directory of the restore directory where the temporary
// directories of restores are made, one per command (ВК13).
const stagingName = ".sard-staging"

// Restore restores the snapshot into the destination of the plan, or leaves
// no trace.
func (e *Engine) Restore(ctx context.Context, repo Repository, req Request, r Reporter) (Output, *Failure) {
	if err := req.Plan.Validate(); err != nil {
		// The code that writes enforces the rules of a plan itself (ADR 0008).
		return Output{}, &Failure{Reason: refusal.TargetInvalid, Stage: StageCheck, Detail: "the restore plan is invalid: " + err.Error()}
	}
	a := &attempt{e: e, ctx: ctx, repo: repo, req: req, r: r, destination: filepath.Clean(req.Plan.Destination), target: e.targetOf(req.Repository)}
	defer a.release()
	r.Progress(agentv1.StepPhase_STEP_PHASE_PREPARING, 0, 0)
	if f := a.prepare(); f != nil {
		return Output{}, a.rollback(f)
	}
	r.Progress(agentv1.StepPhase_STEP_PHASE_RESTORING, 0, 0)
	_, err := repo.RestoreTo(ctx, restic.RestoreRequest{
		SnapshotID: req.SnapshotID, Target: a.stagingPath(), Include: req.Plan.Paths,
		Progress: func(p restic.Progress) {
			r.ProgressFiles(agentv1.StepPhase_STEP_PHASE_RESTORING, p.BytesDone, p.TotalBytes, p.FilesDone, p.TotalFiles)
		},
	})
	if err != nil {
		return Output{}, a.rollback(resticFailure(ctx, err, StageRestore, e.opts.Dir, a.target))
	}
	files, bytes := countFiles(a.stagingPath())
	if f := a.move(); f != nil {
		return Output{}, a.rollback(f)
	}
	r.Log(agentv1.LogLevel_LOG_LEVEL_INFO, "the owners and groups of the snapshot are not restored: the agent runs as an unprivileged service user, so every file belongs to it; permissions and modification times are kept")
	return Output{Target: a.destinationPath(), Files: files, Bytes: bytes}, nil
}

// countFiles is the number and the size of the regular files below dir,
// links not followed. restic counts the directories among the files it
// restores; the console shows the files. An entry that cannot be looked at
// is not counted.
func countFiles(dir string) (files, bytes uint64) {
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			files++
			bytes += uint64(info.Size())
		}
		return nil
	})
	return files, bytes
}

// attempt is one restore in progress.
type attempt struct {
	e           *Engine
	ctx         context.Context
	repo        Repository
	req         Request
	r           Reporter
	destination string // clean
	target      repoinit.Target

	claimed bool
	root    hostsetup.Dir // restore.dir, held
	staging hostsetup.Dir // .sard-staging, held
	// created are the directories this attempt made and removes if they are
	// empty, in the order it made them.
	created []string
	// stagingMade: <command_id> in .sard-staging is this attempt's.
	stagingMade bool
}

func (a *attempt) destinationPath() string {
	return filepath.Join(a.e.opts.Dir, a.destination)
}

func (a *attempt) stagingPath() string {
	return filepath.Join(a.e.opts.Dir, stagingName, a.req.CommandID)
}

func (a *attempt) release() {
	for _, d := range []hostsetup.Dir{a.staging, a.root} {
		if d != nil {
			_ = d.Close()
		}
	}
	if a.claimed {
		a.e.free(a.destination)
	}
}

// checkSnapshot: the repository holds the snapshot, of the source of the
// step, and every path asked for; nothing is written yet.
func (a *attempt) checkSnapshot() *Failure {
	list, err := restore.List(a.ctx, a.repo, a.req.Source)
	if err != nil {
		return asFailure(restore.ListFailure(a.ctx, err, a.target), StageCheck, a.target)
	}
	snap, failure := restore.Select(list, a.req.SnapshotID, a.req.Repository, a.req.Source)
	if failure != nil {
		return &Failure{Reason: failure.Reason, Stage: StageCheck, Detail: failure.Detail}
	}
	if failure := restore.RequirePaths(a.ctx, a.repo, snap, a.req.Plan.Paths, a.target); failure != nil {
		return asFailure(failure, StageCheck, a.target)
	}
	return nil
}

// prepare judges the snapshot and the destination, then makes the temporary
// directory of the restore.
func (a *attempt) prepare() *Failure {
	if f := a.checkSnapshot(); f != nil {
		return f
	}
	if !a.e.claim(a.destination) {
		return &Failure{Reason: refusal.TargetBusy, Stage: StageCheck, Detail: "another restore into " + a.destinationPath() + " is running; wait for it and run the restore again"}
	}
	a.claimed = true
	if f := a.inspect(); f != nil {
		return f
	}
	return a.makeStaging()
}

// rollback removes what the attempt made, by descriptors and without
// following a link: its temporary directory with everything in it, then the
// directories it made, if they are empty. What it did not make stays. What
// it cannot remove turns the failure into CLEANUP_FAILED (ВК15).
func (a *attempt) rollback(f *Failure) *Failure {
	if a.stagingMade {
		if err := hostsetup.RemoveTree(a.staging, a.req.CommandID); err != nil {
			f.Leftover = append(f.Leftover, a.stagingPath())
			f.Cleanup = err.Error()
			a.e.opts.Logger.Error("the rollback of a restore could not remove its temporary directory; remove it by hand",
				"command_id", a.req.CommandID, "path", a.stagingPath(), "error", err)
		}
	}
	for _, p := range slices.Backward(a.created) {
		_ = a.e.opts.FS.Remove(p) // rmdir: a directory with files in it stays
	}
	return f
}

// moveFailure: the destination turned non-empty (ENOTEMPTY), another file
// system (EXDEV) or something else (ВК13).
func moveFailure(err error, destination string) *Failure {
	f := &Failure{Stage: StageMove, Detail: "cannot move the restored directory to " + destination + ": " + err.Error()}
	switch {
	case errors.Is(err, syscall.ENOTEMPTY), errors.Is(err, fs.ErrExist):
		f.Reason = refusal.TargetNotEmpty
		f.Detail = "the destination " + destination + " is not empty: something turned up in it while restic worked and is not replaced"
	case errors.Is(err, hostsetup.ErrCrossDevice), errors.Is(err, syscall.EXDEV):
		f.Reason = refusal.TargetInvalid
		f.Detail = "mount point inside the restore directory: " + destination + " is on another file system than the temporary directory"
	default:
		f.Reason = refusal.TargetWrite
	}
	return f
}

// move puts the restored directory into the destination with one rename.
func (a *attempt) move() *Failure {
	parent, name := filepath.Split(a.destinationPath())
	owner := hostsetup.Make{Parents: a.e.opts.Owner, Last: a.e.opts.Owner}
	dir, created, err := hostsetup.OpenDir(a.e.opts.FS, filepath.Clean(parent), &owner)
	a.created = append(a.created, created...)
	if err != nil {
		return &Failure{Reason: refusal.TargetWrite, Stage: StageMove, Detail: err.Error()}
	}
	defer func() { _ = dir.Close() }()
	if err := a.staging.Move(a.req.CommandID, dir, name); err != nil {
		return moveFailure(err, a.destinationPath())
	}
	a.stagingMade = false // the directory is the destination now
	if err := dir.Sync(); err != nil {
		// The rename happened: the restore did too. Only its durability is in doubt.
		a.e.opts.Logger.Warn("the restored directory is in place, but the directory that holds it could not be synced to disk",
			"command_id", a.req.CommandID, "path", a.destinationPath(), "error", err)
	}
	return nil
}
