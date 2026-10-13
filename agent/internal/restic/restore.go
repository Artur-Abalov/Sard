// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrNoSpace: restic could not write a file because the disk of the target
// is full.
var ErrNoSpace = errors.New("no space left on the target")

const noSpaceCause = "no space left on device"

// RestoreRequest is a restore of repo restore (A8c-2).
type RestoreRequest struct {
	// SnapshotID is the full id of the snapshot, Target the directory.
	SnapshotID, Target string
	// Overwrite lets restic replace files that are in Target already; without
	// it restic never touches a file that exists.
	Overwrite bool
	// Include limits the restore to these paths, taken literally.
	Include []string
	// SFTPCommand, if not empty, is the command restic starts instead of ssh
	// to reach an sftp: repository (sftp.command).
	SFTPCommand string
}

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

// Is: a file that could not be written for lack of space is ErrNoSpace.
func (e *RestoreError) Is(target error) bool {
	if target != ErrNoSpace {
		return false
	}
	for _, it := range e.Items {
		if strings.Contains(it.Message, noSpaceCause) {
			return true
		}
	}
	return false
}

// RestoreTo runs `restic restore` of a whole snapshot, or of the paths in
// Include, into Target. restic runs without a cache and without a lock
// (Н26, Н27): it leaves no file in the cache or the repository, so the
// process may be root, which writes the files of the snapshot with their
// owners.
func (c *CLI) RestoreTo(ctx context.Context, req RestoreRequest) (RestoreSummary, error) {
	if req.SnapshotID == "" || req.Target == "" {
		return RestoreSummary{}, fmt.Errorf("%w: snapshot and target are required", ErrInvalidRequest)
	}
	overwrite := "never"
	if req.Overwrite {
		overwrite = "always"
	}
	args := []string{"restore", "--json", "--no-cache", "--no-lock", "--target", req.Target, "--overwrite", overwrite}
	for _, p := range req.Include {
		args = append(args, "--include", p)
	}
	if req.SFTPCommand != "" {
		args = append(args, "-o", "sftp.command="+req.SFTPCommand)
	}
	var summary RestoreSummary
	res, err := c.runRepo(ctx, call{args: append(args, "--", req.SnapshotID), stdout: func(line []byte) { summary = readSummary(line, summary) }})
	return summary, incomplete(res, err)
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
