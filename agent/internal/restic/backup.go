// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// BackupRequest says what one `restic backup` stores.
type BackupRequest struct {
	Paths    []string
	Excludes []string // restic --exclude patterns
	Tags     []string // no commas: restic splits tags on them
}

// Progress is one restic status report.
type Progress struct {
	BytesDone, TotalBytes uint64
	FilesDone, TotalFiles uint64
	PercentDone           float64 // 0..1
}

// BackupSummary is what a snapshot added to the repository.
type BackupSummary struct {
	SnapshotID   string
	RepositoryID string
	// TotalBytes is the size of the files the snapshot covers.
	TotalBytes uint64
	// AddedBytes is how much the repository grew (after compression);
	// AddedBytesRaw is the same before compression.
	AddedBytes    uint64
	AddedBytesRaw uint64

	FilesNew, FilesChanged, FilesUnmodified uint64
	Start, End                              time.Time
}

// ErrUnreadableSource is matched by a *PartialError.
var ErrUnreadableSource = errors.New("at least one source file could not be read")

// PartialError means the snapshot was written without some files.
type PartialError struct {
	Items []ItemError
}

func (e *PartialError) Error() string {
	return fmt.Sprintf("%s (%d errors)", ErrUnreadableSource, len(e.Items))
}

func (e *PartialError) Is(target error) bool { return target == ErrUnreadableSource }

// ItemError is one file or directory restic could not read.
type ItemError struct {
	Item    string
	During  string // "scan" or "archival"
	Message string
}

// message is any JSON line restic prints; fields are filled per type.
type message struct {
	Type string `json:"message_type"`
	// status
	PercentDone float64 `json:"percent_done"`
	TotalFiles  uint64  `json:"total_files"`
	FilesDone   uint64  `json:"files_done"`
	TotalBytes  uint64  `json:"total_bytes"`
	BytesDone   uint64  `json:"bytes_done"`
	// summary
	SnapshotID          string    `json:"snapshot_id"`
	FilesNew            uint64    `json:"files_new"`
	FilesChanged        uint64    `json:"files_changed"`
	FilesUnmodified     uint64    `json:"files_unmodified"`
	DataAdded           uint64    `json:"data_added"`
	DataAddedPacked     uint64    `json:"data_added_packed"`
	TotalBytesProcessed uint64    `json:"total_bytes_processed"`
	BackupStart         time.Time `json:"backup_start"`
	BackupEnd           time.Time `json:"backup_end"`
	// error
	Error  struct{ Message string } `json:"error"`
	During string                   `json:"during"`
	Item   string                   `json:"item"`
	// exit_error
	Message string `json:"message"`
}

// Backup runs `restic backup --json`. The repository id is read first,
// which also proves the key works before a long backup starts.
func (c *CLI) Backup(ctx context.Context, req BackupRequest, progress func(Progress)) (BackupSummary, error) {
	if err := req.validate(); err != nil {
		return BackupSummary{}, err
	}
	id, err := c.ID(ctx)
	if err != nil {
		return BackupSummary{}, err
	}
	out := &backupOutput{progress: progress, summary: BackupSummary{RepositoryID: id}}
	res, err := c.runRepo(ctx, req.args(), out.line)
	return out.result(res, err)
}

// exitPartial is restic's exit code for a snapshot written without some files.
const exitPartial = 3

// backupOutput reads backup --json stdout.
type backupOutput struct {
	progress func(Progress)
	summary  BackupSummary
}

// result keeps the summary of a snapshot written without some files.
func (o *backupOutput) result(res *result, err error) (BackupSummary, error) {
	if err != nil && res.code != exitPartial {
		return BackupSummary{}, err
	}
	if o.summary.SnapshotID == "" {
		return BackupSummary{}, fmt.Errorf("restic backup: %w: no snapshot in the summary", ErrBadOutput)
	}
	if res.code == exitPartial {
		return o.summary, fmt.Errorf("restic backup: %w", &PartialError{Items: res.items})
	}
	return o.summary, nil
}

func (o *backupOutput) line(line []byte) {
	var msg message
	_ = json.Unmarshal(line, &msg) // anything but JSON has no type
	switch msg.Type {
	case "status":
		if o.progress != nil {
			o.progress(msg.progress())
		}
	case "summary":
		o.summary = msg.summary(o.summary.RepositoryID)
	}
}

func (m message) progress() Progress {
	return Progress{
		BytesDone:   m.BytesDone,
		TotalBytes:  m.TotalBytes,
		FilesDone:   m.FilesDone,
		TotalFiles:  m.TotalFiles,
		PercentDone: m.PercentDone,
	}
}

func (m message) summary(repositoryID string) BackupSummary {
	return BackupSummary{
		SnapshotID:      m.SnapshotID,
		RepositoryID:    repositoryID,
		TotalBytes:      m.TotalBytesProcessed,
		AddedBytes:      m.DataAddedPacked,
		AddedBytesRaw:   m.DataAdded,
		FilesNew:        m.FilesNew,
		FilesChanged:    m.FilesChanged,
		FilesUnmodified: m.FilesUnmodified,
		Start:           m.BackupStart,
		End:             m.BackupEnd,
	}
}

func (r BackupRequest) validate() error {
	switch {
	case len(r.Paths) == 0:
		return fmt.Errorf("%w: no paths", ErrInvalidRequest)
	case slices.Contains(r.Paths, ""):
		return fmt.Errorf("%w: empty path", ErrInvalidRequest)
	case slices.ContainsFunc(r.Tags, badTag):
		return fmt.Errorf("%w: empty tag or tag with a comma", ErrInvalidRequest)
	}
	return nil
}

func badTag(t string) bool { return t == "" || strings.Contains(t, ",") }

// args puts the paths after "--" so a path starting with "-" is not a flag.
func (r BackupRequest) args() []string {
	args := []string{"backup", "--json"}
	for _, t := range r.Tags {
		args = append(args, "--tag", t)
	}
	for _, e := range r.Excludes {
		args = append(args, "--exclude", e)
	}
	return append(append(args, "--"), r.Paths...)
}
