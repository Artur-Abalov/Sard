// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restore

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
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
	// Target is the clean path of the held target, for messages.
	Target string
	// Staging is where restic writes (Р73). Run commits or removes it.
	Staging *Staging
	// Paths limits the restore to these paths of the snapshot.
	Paths []string
	// SFTPCommand is how restic reaches an sftp: repository as the service
	// user (SFTPCommand); empty for any other.
	SFTPCommand string
}

// Run restores the snapshot with restic into the staging directory and then
// moves what was restored into the target; restic gets the full id of the
// snapshot only. When restic could not restore some files what it did
// restore is moved all the same; any other failure of restic removes the
// staging directory with what it holds. A failure says what the target may hold.
func Run(ctx context.Context, r Restorer, req Request, target repoinit.Target) (restic.RestoreSummary, *refusal.Failure) {
	summary, err := r.RestoreTo(ctx, restic.RestoreRequest{
		SnapshotID: req.Snapshot.ID, Staging: req.Staging.Dir().Handle(), Include: req.Paths, SFTPCommand: req.SFTPCommand,
	})
	if err == nil {
		return summary, req.Staging.Commit()
	}
	return summary, req.Staging.finish(restoreFailure(ctx, err, req.Target, target))
}

// maxNamedPaths is how many paths RESTORE_INCOMPLETE names.
const maxNamedPaths = 10

func restoreFailure(ctx context.Context, err error, path string, target repoinit.Target) *refusal.Failure {
	if ctx.Err() == nil {
		if f := targetFailure(err, path, target); f != nil {
			return f
		}
	}
	f := repoinit.FromRestic(ctx, err, target)
	if slices.Contains([]refusal.Reason{refusal.Timeout, refusal.Interrupted, refusal.BackendUnavailable}, f.Reason) {
		f.Detail += "; " + partialNote(path)
	}
	return f
}

// targetFailure: restic could not write the target (no space, quota, read-only
// file system) or could not restore some files; nil for any other failure.
func targetFailure(err error, path string, target repoinit.Target) *refusal.Failure {
	var missing *restic.RestoreError
	switch {
	case errors.Is(err, restic.ErrNoSpace):
		return refusal.Fail(refusal.TargetWrite, "no space left on device (or the quota is used up) while writing to %s; %s", path, partialNote(path))
	case errors.Is(err, restic.ErrReadOnly):
		return refusal.Fail(refusal.TargetWrite, "the file system of %s is read-only; %s", path, partialNote(path))
	case errors.As(err, &missing):
		return refusal.Fail(refusal.RestoreIncomplete, "%s", target.Scrub(incompleteText(missing.Paths(), path)))
	}
	return nil
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

// CheckSetpriv: an sftp: repository is restored with ssh started by setpriv
// as the service user (SFTPCommand), so setpriv must be in the PATH of
// restic; has says whether it is. The other kinds of storage do not need it (Р77).
func CheckSetpriv(kind string, has func(program string) bool) *refusal.Failure {
	if kind != "sftp" || has("setpriv") {
		return nil
	}
	return refusal.Fail(refusal.SetprivMissing,
		"setpriv is not in the PATH of restic; restoring from an sftp: repository starts ssh as the service user with it. It is of the package util-linux: sudo apt-get install util-linux (Debian, Ubuntu) or sudo dnf install util-linux (RHEL family). Repositories of other kinds of storage do not need it")
}

// InputsFailure explains why restic may not run as root against the
// repository: a kind of storage without an allowlist (BACKEND_NOT_SUPPORTED),
// a variable of the env_file that it may not get (ENV_FILE_INVALID: the file,
// the line and the name, never the value) or a password file that A1 rejects.
func InputsFailure(err error) *refusal.Failure {
	var unsupported *restic.BackendNotSupportedError
	switch {
	case errors.As(err, &unsupported):
		return refusal.Fail(refusal.BackendNotSupported, "the repository is of kind %s: repo restore runs restic as root, and for this kind of storage there is no list of variables of env_file it may get; restore it with restic by hand, or ask for the kind to be supported", unsupported.Kind)
	case errors.Is(err, restic.ErrInvalidEnvFile):
		return refusal.Fail(refusal.EnvFileInvalid, "%s", strings.TrimPrefix(err.Error(), restic.ErrInvalidEnvFile.Error()+": "))
	case errors.Is(err, fs.ErrNotExist) && errors.Is(err, restic.ErrPasswordFile):
		return refusal.Fail(refusal.PasswordFileMissing, "%v", err)
	case errors.Is(err, fs.ErrNotExist):
		return refusal.Fail(refusal.EnvFileMissing, "%v", err)
	}
	return &refusal.Failure{Reason: refusal.SecretFileRejected, Class: refusal.ClassOf(refusal.SecretFileRejected), Detail: err.Error()}
}
