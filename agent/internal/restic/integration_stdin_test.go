// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:build integration

package restic_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// randomStream writes size pseudo-random bytes, 64 KiB at a time, and
// returns early when ctx is done.
func randomStream(size int) (data []byte, stream func(context.Context, io.Writer) error) {
	data = make([]byte, size)
	_, _ = rand.NewChaCha8([32]byte{2}).Read(data)
	return data, func(ctx context.Context, w io.Writer) error {
		for rest := data; len(rest) > 0; rest = rest[min(len(rest), 64<<10):] {
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, err := w.Write(rest[:min(len(rest), 64<<10)]); err != nil {
				return err
			}
		}
		return nil
	}
}

// snapshots counts the snapshots of a local repository.
func snapshots(t *testing.T, r realRepo) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(r.dir, "snapshots"))
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

func initRepo(t *testing.T) realRepo {
	t.Helper()
	r := newRealRepo(t)
	if _, err := r.cli.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return r
}

// A stream stored from stdin restores as one file with the same bytes.
func TestIntegrationStdinRoundTrip(t *testing.T) {
	ctx := context.Background()
	r := initRepo(t)
	data, stream := randomStream(dataSizeMB(t) << 20)
	var last restic.Progress
	sum, err := r.cli.Backup(ctx, restic.BackupRequest{Stdin: stream, StdinFilename: "dump/db.sql"}, func(p restic.Progress) { last = p })
	if err != nil || sum.SnapshotID == "" || sum.TotalBytes != uint64(len(data)) || sum.FilesNew != 1 {
		t.Fatalf("Backup = %+v, %v", sum, err)
	}
	t.Logf("stdin backup: %+v, last progress %+v", sum, last)
	target := t.TempDir()
	if err := r.cli.Restore(ctx, sum.SnapshotID, target); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(target, "dump", "db.sql"))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("restored %d bytes (%v), want %d", len(got), err, len(data))
	}
}

// A stream failing half-way leaves no snapshot, no lock and no restic.
func TestIntegrationFailedStreamStoresNothing(t *testing.T) {
	r := initRepo(t)
	_, stream := randomStream(4 << 20)
	dumpFailed := errors.New("dump failed")
	req := restic.BackupRequest{StdinFilename: "db.sql", Stdin: func(ctx context.Context, w io.Writer) error {
		if err := stream(ctx, w); err != nil {
			return err
		}
		return dumpFailed
	}}
	if _, err := r.cli.Backup(context.Background(), req, nil); !errors.Is(err, dumpFailed) {
		t.Fatalf("err = %v", err)
	}
	assertNothingLeft(t, r)
}

// Cancelling during the stream stops the stream and restic.
func TestIntegrationCancelledStream(t *testing.T) {
	r := initRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopped := make(chan error, 1)
	req := restic.BackupRequest{StdinFilename: "db.sql", Stdin: func(ctx context.Context, w io.Writer) error {
		chunk := make([]byte, 64<<10)
		for written := 0; ; written += len(chunk) {
			if written == 2<<20 {
				cancel()
			}
			if _, err := w.Write(chunk); err != nil {
				stopped <- err
				return err
			}
			if err := ctx.Err(); err != nil {
				stopped <- err
				return err
			}
		}
	}}
	if _, err := r.cli.Backup(ctx, req, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if err := <-stopped; err == nil {
		t.Fatal("the stream did not stop")
	}
	assertNothingLeft(t, r)
}

func assertNothingLeft(t *testing.T, r realRepo) {
	t.Helper()
	if n := snapshots(t, r); n != 0 {
		t.Errorf("%d snapshots stored", n)
	}
	if locks, err := os.ReadDir(filepath.Join(r.dir, "locks")); err != nil || len(locks) != 0 {
		t.Errorf("locks = %v, %v", locks, err)
	}
	if pids := running(pinnedBinary(t)); len(pids) != 0 {
		t.Errorf("restic still running: %v", pids)
	}
}
