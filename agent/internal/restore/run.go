// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restore

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// Restorer is the restic restore of one repository; restic.CLI is one.
type Restorer interface {
	RestoreTo(ctx context.Context, req restic.RestoreRequest) (restic.RestoreSummary, error)
}

// PathFinder says whether a snapshot holds a path; restic.CLI is one.
type PathFinder interface {
	HasPath(ctx context.Context, snapshotID, path string) (bool, error)
}

// Request is the restore of a snapshot of a directory (Р61).
type Request struct {
	Snapshot Snapshot
	// Target is the clean path of the held target.
	Target    string
	Overwrite bool
	// Paths limits the restore to these paths of the snapshot.
	Paths []string
	// SFTPCommand is how restic reaches an sftp: repository as the service
	// user (SFTPCommand); empty for any other.
	SFTPCommand string
}

// Run restores the snapshot into the target with restic; restic gets the
// full id of the snapshot only. A failure says what the target may hold.
func Run(ctx context.Context, r Restorer, req Request, target repoinit.Target) (restic.RestoreSummary, *refusal.Failure) {
	summary, err := r.RestoreTo(ctx, restic.RestoreRequest{
		SnapshotID: req.Snapshot.ID, Target: req.Target, Overwrite: req.Overwrite, Include: req.Paths, SFTPCommand: req.SFTPCommand,
	})
	if err != nil {
		return summary, restoreFailure(ctx, err, req.Target, target)
	}
	return summary, nil
}

// maxNamedPaths is how many paths RESTORE_INCOMPLETE names.
const maxNamedPaths = 10

func restoreFailure(ctx context.Context, err error, path string, target repoinit.Target) *refusal.Failure {
	var missing *restic.RestoreError
	switch {
	case ctx.Err() == nil && errors.Is(err, restic.ErrNoSpace):
		return refusal.Fail(refusal.TargetWrite, "no space left on device while writing to %s; %s", path, partialNote(path))
	case ctx.Err() == nil && errors.As(err, &missing):
		return refusal.Fail(refusal.RestoreIncomplete, "%s", target.Scrub(incompleteText(missing.Paths(), path)))
	}
	f := repoinit.FromRestic(ctx, err, target)
	if slices.Contains([]refusal.Reason{refusal.Timeout, refusal.Interrupted, refusal.BackendUnavailable}, f.Reason) {
		f.Detail += "; " + partialNote(path)
	}
	return f
}

func partialNote(path string) string {
	return "partially restored files may remain in " + path
}

func incompleteText(paths []string, target string) string {
	count := len(paths)
	head := fmt.Sprintf("restic could not restore %d paths", count)
	if count > maxNamedPaths {
		head += fmt.Sprintf(" (the first %d)", maxNamedPaths)
		paths = paths[:maxNamedPaths]
	}
	quoted := make([]string, len(paths))
	for i, p := range paths {
		quoted[i] = fmt.Sprintf("%q", p)
	}
	return fmt.Sprintf("%s: %s; everything else was restored and stays in %s", head, strings.Join(quoted, ", "), target)
}

// RequirePaths: the snapshot holds every path (Р61); a path it does not hold
// is PATH_NOT_IN_SNAPSHOT, found before anything is written.
func RequirePaths(ctx context.Context, finder PathFinder, s Snapshot, paths []string, target repoinit.Target) *refusal.Failure {
	for _, p := range paths {
		found, err := finder.HasPath(ctx, s.ID, p)
		if err != nil {
			return repoinit.FromRestic(ctx, err, target)
		}
		if !found {
			return refusal.Fail(refusal.PathNotInSnapshot, "the path %s is not in the snapshot %s; nothing was restored", p, s.ShortID)
		}
	}
	return nil
}

// SFTPCommand is the command restic starts instead of ssh when it runs as
// root: ssh itself runs as the service user, with its ~/.ssh, and root never
// opens the private key (Р63, ADR 0054). setpriv is of util-linux.
func SFTPCommand(as restic.RunAs, a hostsetup.SFTPAddress) string {
	words := []string{"setpriv", fmt.Sprintf("--reuid=%d", as.UID), fmt.Sprintf("--regid=%d", as.GID), "--clear-groups", "ssh", "-p", fmt.Sprint(a.Port)}
	if a.User != "" {
		words = append(words, "-l", a.User)
	}
	return strings.Join(append(words, "-s", "--", a.Host, "sftp"), " ")
}
