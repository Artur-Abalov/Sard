// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
)

// ErrNoSpace: restic could not write a file because the disk of the target
// is full or the quota of the user is used up (ENOSPC, EDQUOT, Р76).
var ErrNoSpace = errors.New("no space left on the target")

// ErrReadOnly: the file system of the target is read-only (EROFS, Р76).
var ErrReadOnly = errors.New("the file system of the target is read-only")

// targetCauses are the texts of the errors of the system that say the target
// cannot be written, and the error each stands for.
var targetCauses = []struct {
	text string
	err  error
}{
	{"no space left on device", ErrNoSpace},
	{"disk quota exceeded", ErrNoSpace},
	{"read-only file system", ErrReadOnly},
}

// targetCause is the error for a text of restic that says the target cannot
// be written; nil for any other text.
func targetCause(msg string) error {
	for _, c := range targetCauses {
		if strings.Contains(msg, c.text) {
			return c.err
		}
	}
	return nil
}

// RestoreRequest is a restore of repo restore (A8c-2).
type RestoreRequest struct {
	// SnapshotID is the full id of the snapshot, Target the directory.
	SnapshotID, Target string
	// Staging, if not nil, is an open directory restic restores into, as its
	// subdirectory root, instead of Target: it is inherited as descriptor 3
	// and restic gets --target /proc/self/fd/3/root. restic then writes only
	// below a directory that nobody but root can change (Р73).
	Staging *os.File
	// Include limits the restore to these paths, taken literally.
	Include []string
	// SFTPCommand, if not empty, is the command restic starts instead of ssh
	// to reach an sftp: repository (sftp.command).
	SFTPCommand string
}

// stagingTarget is the root directory of the staging directory, which restic
// inherits as descriptor 3. The bare /proc/self/fd/3 does not work: restic
// removes the target's magic link when it makes the directory (ADR 0059).
const stagingTarget = "/proc/self/fd/3/root"

// RestoreSummary is what restic reports of a restore.
type RestoreSummary struct{ Files, Bytes uint64 }

// RestoreError means restic restored the snapshot without some files; what
// it could restore stays in the target.
type RestoreError struct{ Items []ItemError }

// Paths are the distinct paths restic reported, in the order of the first report.
func (e *RestoreError) Paths() []string { return distinctPaths(e.Items) }

func (e *RestoreError) Error() string {
	return fmt.Sprintf("restic could not restore %d paths", len(e.Paths()))
}

// Is: a file that could not be written for lack of space is ErrNoSpace, one
// on a read-only file system ErrReadOnly.
func (e *RestoreError) Is(target error) bool {
	return slices.ContainsFunc(e.Items, func(it ItemError) bool {
		return targetCause(it.Message) == target
	})
}

// RestoreTo runs `restic restore` of a whole snapshot, or of the paths in
// Include, into Target. restic runs without a cache and without a lock
// (Н26, Н27): it leaves no file in the cache or the repository, so the
// process may be root, which writes the files of the snapshot with their
// owners.
func (c *CLI) RestoreTo(ctx context.Context, req RestoreRequest) (RestoreSummary, error) {
	target, extra := req.Target, []*os.File(nil)
	if req.Staging != nil {
		target, extra = stagingTarget, []*os.File{req.Staging}
	}
	if req.SnapshotID == "" || target == "" || (c.opts.AsRoot && req.Staging == nil) {
		return RestoreSummary{}, fmt.Errorf("%w: snapshot and target are required", ErrInvalidRequest)
	}
	var summary RestoreSummary
	res, err := c.runRepo(ctx, call{args: restoreArgs(req, target), extra: extra, stdout: func(line []byte) { summary = readSummary(line, summary) }})
	return summary, incomplete(res, err)
}

// restoreArgs are the arguments of `restic restore` of the request.
func restoreArgs(req RestoreRequest, target string) []string {
	args := []string{"restore", "--json", "--no-cache", "--no-lock", "--target", target, "--overwrite", "never"}
	for _, p := range req.Include {
		args = append(args, "--include", p)
	}
	if req.SFTPCommand != "" {
		args = append(args, "-o", "sftp.command="+req.SFTPCommand)
	}
	return append(args, "--", req.SnapshotID)
}

// readSummary is the summary the line holds, or the one given.
func readSummary(line []byte, current RestoreSummary) RestoreSummary {
	var msg struct {
		Type          string `json:"message_type"`
		FilesRestored uint64 `json:"files_restored"`
		BytesRestored uint64 `json:"bytes_restored"`
	}
	if json.Unmarshal(line, &msg) != nil || msg.Type != "summary" {
		return current
	}
	return RestoreSummary{Files: msg.FilesRestored, Bytes: msg.BytesRestored}
}

// incomplete turns the exit of restic with errors of single files into a
// *RestoreError; any other failure stays as it is.
func incomplete(res *result, err error) error {
	var exit *ExitError
	if err == nil || len(res.items) == 0 || !errors.As(err, &exit) || fatalKind(res.cause()) != nil {
		return err
	}
	return fmt.Errorf("%s: %w", res.cmd, &RestoreError{Items: res.items})
}

// HasPath runs `restic ls` of path in the snapshot: whether the snapshot
// holds the file or the directory, listed without the lock.
func (c *CLI) HasPath(ctx context.Context, snapshotID, path string) (bool, error) {
	found := false
	_, err := c.runRepo(ctx, call{args: []string{"ls", "--json", "--no-lock", "--", snapshotID, path}, stdout: func(line []byte) {
		var msg struct {
			Type string `json:"message_type"`
		}
		found = found || json.Unmarshal(line, &msg) == nil && msg.Type == "node"
	}})
	return found && err == nil, err
}

// Dump runs `restic dump` of one file of the snapshot and writes its content
// to w as it comes (Р62). The lock is not taken (Н26). The caller judges w:
// a failure of w ends restic with an error of its own, which w remembers.
func (c *CLI) Dump(ctx context.Context, snapshotID, path string, w io.Writer) error {
	if snapshotID == "" || path == "" {
		return fmt.Errorf("%w: snapshot and path are required", ErrInvalidRequest)
	}
	_, err := c.runRepo(ctx, call{args: []string{"dump", "--no-lock", "--", snapshotID, path}, stdoutTo: w})
	return err
}
