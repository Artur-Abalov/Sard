// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// write returns a stream that writes data and ends.
func write(data string) func(context.Context, io.Writer) error {
	return func(_ context.Context, w io.Writer) error {
		_, err := io.WriteString(w, data)
		return err
	}
}

// Golden: restic 0.19.1 `backup --json --stdin` of "hello stdin\n".
func TestBackupFromStdinStoresTheStreamUnderTheFilename(t *testing.T) {
	f := backupFixture(t, reply{stdout: "backup-stdin.stdout", readStdin: true})
	req := restic.BackupRequest{Stdin: write("hello stdin\n"), StdinFilename: "db.sql", Tags: []string{"run=7"}}
	sum, err := f.build().Backup(context.Background(), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := restic.BackupSummary{
		SnapshotID:    "34c3de58d938fd95651bab4f5d9cd0ea414b90deaffe23156275a51f7ac01367",
		RepositoryID:  goldenRepoID,
		TotalBytes:    12,
		AddedBytes:    344,
		AddedBytesRaw: 295,
		FilesNew:      1,
		Start:         mustTime(t, "2026-09-29T09:18:09.848544416Z"),
		End:           mustTime(t, "2026-09-29T09:18:10.569729427Z"),
	}
	if sum != want {
		t.Errorf("summary = %+v\nwant      %+v", sum, want)
	}
	args := []string{"backup", "--json", "--tag", "run=7", "--stdin", "--stdin-filename=db.sql"}
	if got := f.exec.call("backup").Args; !slices.Equal(got, args) {
		t.Errorf("args = %q, want %q", got, args)
	}
	if string(f.exec.stdin) != "hello stdin\n" {
		t.Errorf("restic read %q", f.exec.stdin)
	}
}

// A stream larger than a pipe buffer reaches restic whole.
func TestBackupFromStdinPassesALargeStream(t *testing.T) {
	f := backupFixture(t, reply{stdout: "backup-stdin.stdout", readStdin: true})
	data := strings.Repeat("0123456789abcdef", 1<<16) // 1 MiB
	req := restic.BackupRequest{Stdin: write(data), StdinFilename: "big"}
	if _, err := f.build().Backup(context.Background(), req, nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(f.exec.stdin, []byte(data)) {
		t.Errorf("restic read %d bytes, want %d", len(f.exec.stdin), len(data))
	}
}

// A failed stream must not end in EOF: restic would store what it got as
// a complete snapshot. restic is stopped instead, and the stream's error
// is the step's error.
func TestAFailedStreamStopsResticWithoutASnapshot(t *testing.T) {
	f := backupFixture(t, reply{stdout: "backup-stdin.stdout", readStdin: true})
	dumpFailed := errors.New("pg_dump exited with 1")
	req := restic.BackupRequest{StdinFilename: "db.sql", Stdin: func(_ context.Context, w io.Writer) error {
		_, _ = io.WriteString(w, "partial")
		return dumpFailed
	}}
	sum, err := f.build().Backup(context.Background(), req, nil)
	if !errors.Is(err, dumpFailed) || err.Error() != "restic backup: stream: pg_dump exited with 1" {
		t.Fatalf("err = %v", err)
	}
	if sum != (restic.BackupSummary{}) {
		t.Errorf("summary = %+v, want none", sum)
	}
	if !f.exec.killed {
		t.Error("restic reached EOF before it was stopped")
	}
}

// When restic fails, a stream still writing is unblocked and restic's
// error is the step's error.
func TestResticFailingDuringTheStreamEndsTheStream(t *testing.T) {
	f := backupFixture(t, reply{stderr: "wrong-password.stderr", code: 12})
	streamErr := make(chan error, 1)
	req := restic.BackupRequest{StdinFilename: "db.sql", Stdin: func(ctx context.Context, w io.Writer) error {
		_, err := w.Write(make([]byte, 1<<20)) // more than a pipe holds
		streamErr <- err
		return err
	}}
	if _, err := f.build().Backup(context.Background(), req, nil); !errors.Is(err, restic.ErrWrongPassword) {
		t.Fatalf("err = %v", err)
	}
	if err := <-streamErr; err == nil {
		t.Error("the stream wrote 1 MiB into a restic that had exited")
	}
}

// Cancelling the step stops the stream and restic; no snapshot is stored.
func TestCancellingDuringTheStreamStopsBoth(t *testing.T) {
	f := backupFixture(t, reply{stdout: "backup-stdin.stdout", readStdin: true})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := restic.BackupRequest{StdinFilename: "db.sql", Stdin: func(ctx context.Context, w io.Writer) error {
		_, _ = io.WriteString(w, "partial")
		cancel()
		<-ctx.Done()
		return ctx.Err()
	}}
	sum, err := f.build().Backup(ctx, req, nil)
	if !errors.Is(err, context.Canceled) || sum != (restic.BackupSummary{}) {
		t.Fatalf("Backup = %+v, %v", sum, err)
	}
	if !f.exec.killed {
		t.Error("restic reached EOF before it was stopped")
	}
}

// A stream that ignores cancellation and returns nil must not close stdin:
// restic would take the EOF for the end of a complete dump.
func TestAStreamIgnoringCancellationDoesNotEndInEOF(t *testing.T) {
	f := backupFixture(t, reply{stdout: "backup-stdin.stdout", readStdin: true})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := restic.BackupRequest{StdinFilename: "db.sql", Stdin: func(ctx context.Context, w io.Writer) error {
		_, _ = io.WriteString(w, "partial")
		cancel()
		<-ctx.Done()
		return nil
	}}
	if _, err := f.build().Backup(ctx, req, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if !f.exec.killed {
		t.Error("restic reached EOF before it was stopped")
	}
}

func TestBackupRejectsInvalidStdinRequests(t *testing.T) {
	stream := write("x")
	for name, req := range map[string]restic.BackupRequest{
		"stdin without a filename": {Stdin: stream},
		"stdin and paths":          {Stdin: stream, StdinFilename: "db.sql", Paths: []string{"/srv"}},
		"stdin and excludes":       {Stdin: stream, StdinFilename: "db.sql", Excludes: []string{"*.tmp"}},
		"stdin with a bad tag":     {Stdin: stream, StdinFilename: "db.sql", Tags: []string{"a,b"}},
		"a filename without stdin": {Paths: []string{"/srv"}, StdinFilename: "db.sql"},
	} {
		f := backupFixture(t, reply{})
		if _, err := f.build().Backup(context.Background(), req, nil); !errors.Is(err, restic.ErrInvalidRequest) {
			t.Errorf("%s: err = %v", name, err)
		}
		if len(f.exec.calls) != 0 {
			t.Errorf("%s: restic ran for an invalid request", name)
		}
	}
}
