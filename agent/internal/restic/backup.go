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
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// BackupRequest says what one `restic backup` stores: Paths, or the
// stream Stdin writes, stored as one file named StdinFilename.
type BackupRequest struct {
	Paths    []string
	Excludes []string // restic --exclude patterns
	Tags     []string // no commas: restic splits tags on them
	// OneFileSystem keeps restic from crossing into other file systems.
	OneFileSystem bool
	// Stdin writes the content to w, restic's stdin, and must return when
	// ctx is done. Only a nil return ends the stream with EOF; after an
	// error or cancellation restic is stopped and stores no snapshot.
	Stdin         func(ctx context.Context, w io.Writer) error
	StdinFilename string
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

// maxNamedPaths is how many unreadable paths the error text names.
const maxNamedPaths = 10

// Error counts the distinct paths restic reported, in the order of their
// first report (a path that failed at scan and at archival counts once),
// and names the first ten, quoted, so the text is one line.
func (e *PartialError) Error() string {
	var paths []string
	seen := make(map[string]bool)
	for _, it := range e.Items {
		if !seen[it.Item] {
			seen[it.Item] = true
			paths = append(paths, it.Item)
		}
	}
	head := fmt.Sprintf("unreadable paths (%d)", len(paths))
	if len(paths) > maxNamedPaths {
		head += fmt.Sprintf(", first %d", maxNamedPaths)
		paths = paths[:maxNamedPaths]
	}
	for i, p := range paths {
		paths[i] = fmt.Sprintf("%q", p)
	}
	return fmt.Sprintf("%s: %s: %s", ErrUnreadableSource, head, strings.Join(paths, ", "))
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
	if req.Stdin == nil {
		return out.result(c.runRepo(ctx, call{args: req.args(), stdout: out.line}))
	}
	return out.result(c.backupStdin(ctx, req, out.line))
}

// streamError marks a cancellation of restic caused by a failed stream.
type streamError struct{ err error }

func (e *streamError) Error() string { return e.err.Error() }

// stopped is the line restic prints when SIGTERM has cancelled its work;
// from then on it stores no snapshot.
var stopped = regexp.MustCompile(`signal \w+ received, cleaning up`)

// backupStdin runs restic with the read end of a pipe as stdin while
// req.Stdin writes into the other end. Closing the write end is EOF, which
// restic takes for the end of complete data, so it is closed early only
// after a successful stream. When restic is being stopped instead, it is
// closed once restic confirms SIGTERM: restic 0.19 keeps reading stdin
// until EOF even then. The read end is closed when restic has exited, so a
// stream still writing fails.
func (c *CLI) backupStdin(ctx context.Context, req BackupRequest, stdout func([]byte)) (*result, error) {
	p, err := newStdinPipe()
	if err != nil {
		return &result{}, fmt.Errorf("restic backup: %w", err)
	}
	defer p.end()
	resticCtx, stopRestic := context.WithCancelCause(ctx)
	defer stopRestic(nil)
	streamCtx, stopStream := context.WithCancel(ctx)
	go p.feed(streamCtx, req.Stdin, stopRestic)
	res, err := c.runRepo(resticCtx, call{args: req.args(), stdout: stdout, stdin: p.r, stderr: p.endOnStop(resticCtx)})
	// Read before the read end closes: a stream that fails on the closed
	// pipe afterwards did not cause restic to stop.
	var failed *streamError
	causedByStream := ctx.Err() == nil && errors.As(context.Cause(resticCtx), &failed)
	_ = p.r.Close()
	stopStream()
	<-p.fed
	if causedByStream {
		return res, fmt.Errorf("restic backup: stream: %w", failed.err)
	}
	return res, err
}

// stdinPipe carries a stream to restic's stdin.
type stdinPipe struct {
	r, w *os.File
	once sync.Once
	fed  chan struct{} // closed when the stream has returned
}

func newStdinPipe() (*stdinPipe, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	return &stdinPipe{r: r, w: w, fed: make(chan struct{})}, nil
}

// end closes the write end: restic reads EOF.
func (p *stdinPipe) end() { p.once.Do(func() { _ = p.w.Close() }) }

// feed runs the stream. Only a stream that succeeded before ctx ended
// gets EOF; a failed one stops restic instead.
func (p *stdinPipe) feed(ctx context.Context, stream func(context.Context, io.Writer) error, stopRestic context.CancelCauseFunc) {
	defer close(p.fed)
	err := stream(ctx, p.w)
	switch {
	case err != nil:
		stopRestic(&streamError{err})
	case ctx.Err() == nil:
		p.end()
	}
}

// endOnStop ends the stream once restic, being stopped, confirms SIGTERM.
func (p *stdinPipe) endOnStop(resticCtx context.Context) func(line []byte) {
	return func(line []byte) {
		if resticCtx.Err() != nil && stopped.Match(line) {
			p.end()
		}
	}
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
	if slices.ContainsFunc(r.Tags, badTag) {
		return fmt.Errorf("%w: empty tag or tag with a comma", ErrInvalidRequest)
	}
	if r.Stdin != nil {
		return r.validateStdin()
	}
	switch {
	case len(r.Paths) == 0:
		return fmt.Errorf("%w: no paths", ErrInvalidRequest)
	case slices.Contains(r.Paths, ""):
		return fmt.Errorf("%w: empty path", ErrInvalidRequest)
	case r.StdinFilename != "":
		return fmt.Errorf("%w: stdin filename without stdin", ErrInvalidRequest)
	}
	return nil
}

func (r BackupRequest) validateStdin() error {
	switch {
	case r.StdinFilename == "":
		return fmt.Errorf("%w: stdin without a filename", ErrInvalidRequest)
	case len(r.Paths) > 0 || len(r.Excludes) > 0:
		return fmt.Errorf("%w: stdin with paths or excludes", ErrInvalidRequest)
	}
	return nil
}

func badTag(t string) bool { return t == "" || strings.Contains(t, ",") }

// lockWait is how long restic waits for a locked repository before it
// gives up with exit code 11 (A6b Ф10); the step timeout applies meanwhile.
const lockWait = "5m"

// args puts the paths after "--" so a path starting with "-" is not a flag.
// The stdin filename is joined to its flag, so "-x" is not parsed as one.
func (r BackupRequest) args() []string {
	args := []string{"backup", "--json", "--retry-lock", lockWait}
	for _, t := range r.Tags {
		args = append(args, "--tag", t)
	}
	if r.Stdin != nil {
		return append(args, "--stdin", "--stdin-filename="+r.StdinFilename)
	}
	if r.OneFileSystem {
		args = append(args, "--one-file-system")
	}
	for _, e := range r.Excludes {
		args = append(args, "--exclude", e)
	}
	return append(append(args, "--"), r.Paths...)
}
