// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

func backupFixture(t *testing.T, backup reply) *fixture {
	return newFixture(t, map[string]reply{"cat": catConfig, "backup": backup})
}

var request = restic.BackupRequest{Paths: []string{"/srv/data"}}

// Golden: 240 MB backup with a status line every ~100 ms.
func TestBackupReturnsTheSummary(t *testing.T) {
	f := backupFixture(t, reply{stdout: "backup-progress.stdout"})
	sum, err := f.build().Backup(context.Background(), request, nil)
	want := restic.BackupSummary{
		SnapshotID:    "00174f77f214115e5b9b0a492e0cf23fc85411e93b42d5cd81c15b6512a6478a",
		RepositoryID:  goldenRepoID,
		TotalBytes:    240000000,
		AddedBytes:    240025786,
		AddedBytesRaw: 240012629,
		FilesNew:      4,
		Start:         mustTime(t, "2026-09-27T09:45:30.998805423Z"),
		End:           mustTime(t, "2026-09-27T09:45:32.92193764Z"),
	}
	if err != nil || sum != want {
		t.Fatalf("summary = %+v, err = %v\nwant      %+v", sum, err, want)
	}
}

func TestBackupReportsProgress(t *testing.T) {
	f := backupFixture(t, reply{stdout: "backup-progress.stdout"})
	var progress []restic.Progress
	if _, err := f.build().Backup(context.Background(), request, func(p restic.Progress) { progress = append(progress, p) }); err != nil {
		t.Fatal(err)
	}
	if len(progress) != 12 {
		t.Fatalf("progress calls = %d, want 12", len(progress))
	}
	first := restic.Progress{BytesDone: 15428342, TotalBytes: 240000000, TotalFiles: 4, PercentDone: 0.06428475833333333}
	// restic 0.19.1 prints no 100% status before the summary.
	last := restic.Progress{BytesDone: 236969023, TotalBytes: 240000000, FilesDone: 3, TotalFiles: 4, PercentDone: 0.9873709291666667}
	if progress[0] != first || progress[11] != last {
		t.Errorf("progress first = %+v, last = %+v", progress[0], progress[11])
	}
	for i := 1; i < len(progress); i++ {
		if progress[i].BytesDone < progress[i-1].BytesDone {
			t.Errorf("progress goes back at %d: %+v", i, progress[i])
		}
	}
}

func TestBackupSummaryOfAnUnchangedBackup(t *testing.T) {
	f := backupFixture(t, reply{stdout: "backup-again.stdout"})
	sum, err := f.build().Backup(context.Background(), request, nil)
	if err != nil || sum.FilesUnmodified != 2 || sum.FilesNew != 0 || sum.FilesChanged != 0 ||
		sum.AddedBytes != 288 || sum.AddedBytesRaw != 348 || sum.TotalBytes != 12 {
		t.Fatalf("summary = %+v, err = %v", sum, err)
	}
}

func TestBackupArguments(t *testing.T) {
	f := backupFixture(t, reply{stdout: "backup-ok.stdout"})
	req := restic.BackupRequest{
		Paths:    []string{"/srv/data", "-odd-name"},
		Excludes: []string{"*.tmp", "/srv/data/cache"},
		Tags:     []string{"sard", "job:nightly"},
	}
	if _, err := f.build().Backup(context.Background(), req, nil); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"backup", "--json",
		"--tag", "sard", "--tag", "job:nightly",
		"--exclude", "*.tmp", "--exclude", "/srv/data/cache",
		"--", "/srv/data", "-odd-name",
	}
	if got := f.exec.call("backup").Args; !slices.Equal(got, want) {
		t.Fatalf("args = %q", got)
	}
}

// Golden: two unreadable items; restic still writes a snapshot and exits 3.
func TestBackupWithUnreadableFilesReturnsSummaryAndPartialError(t *testing.T) {
	f := backupFixture(t, reply{stdout: "backup-read-errors.stdout", stderr: "backup-read-errors.stderr", code: 3})
	sum, err := f.build().Backup(context.Background(), request, nil)
	if sum.SnapshotID != "6c719cbc95d86f49b63cf9535bad29ad721287277e8bc419bc7168a82fd1ad01" || sum.RepositoryID != goldenRepoID {
		t.Errorf("summary = %+v", sum)
	}
	var partial *restic.PartialError
	if !errors.Is(err, restic.ErrUnreadableSource) || !errors.As(err, &partial) {
		t.Fatalf("err = %v", err)
	}
	want := []restic.ItemError{
		{Item: "/tmp/sardcap2/data/locked", During: "scan", Message: "openfile for readdirnames failed: open /tmp/sardcap2/data/locked: permission denied"},
		{Item: "/tmp/sardcap2/data/locked", During: "archival", Message: "openfile for readdirnames failed: open /tmp/sardcap2/data/locked: permission denied"},
		{Item: "/tmp/sardcap2/data/sub/unreadable.txt", During: "archival", Message: "open /tmp/sardcap2/data/sub/unreadable.txt: permission denied"},
	}
	if !slices.Equal(partial.Items, want) {
		t.Errorf("items = %+v", partial.Items)
	}
	if err.Error() != "restic backup: at least one source file could not be read (3 errors)" {
		t.Errorf("text = %q", err.Error())
	}
	if len(f.stderr) != 4 {
		t.Errorf("stderr lines = %d, want 4", len(f.stderr))
	}
}

func TestBackupExitingWithoutSummaryIsAnError(t *testing.T) {
	cases := map[string]reply{
		"exit 0, no output": {},
		"exit 3, no output": {code: 3},
		"exit 0, no id":     {stdout: "cat-config.json"},
	}
	for name, r := range cases {
		f := backupFixture(t, r)
		if _, err := f.build().Backup(context.Background(), request, nil); !errors.Is(err, restic.ErrBadOutput) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestBackupFailure(t *testing.T) {
	f := backupFixture(t, reply{stderr: "backup-missing.stderr", code: 1})
	_, err := f.build().Backup(context.Background(), request, nil)
	if err == nil || err.Error() != "restic backup: exit code 1: Fatal: all source directories/files do not exist" {
		t.Fatalf("err = %v", err)
	}
}

func TestBackupCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := backupFixture(t, reply{stdout: "backup-progress.stdout", stderr: "backup-sigterm.stderr", code: 130, during: cancel})
	if _, err := f.build().Backup(ctx, request, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestBackupStopsWhenTheRepositoryCannotBeOpened(t *testing.T) {
	f := newFixture(t, map[string]reply{"cat": {stderr: "wrong-password.stderr", code: 12}})
	if _, err := f.build().Backup(context.Background(), request, nil); !errors.Is(err, restic.ErrWrongPassword) {
		t.Fatalf("err = %v", err)
	}
}

func TestBackupRejectsInvalidRequests(t *testing.T) {
	cases := map[string]restic.BackupRequest{
		"no paths":     {},
		"empty path":   {Paths: []string{""}},
		"comma in tag": {Paths: []string{"/a"}, Tags: []string{"a,b"}},
		"empty tag":    {Paths: []string{"/a"}, Tags: []string{""}},
	}
	for name, req := range cases {
		f := newFixture(t, nil)
		if _, err := f.build().Backup(context.Background(), req, nil); !errors.Is(err, restic.ErrInvalidRequest) || len(f.exec.calls) != 0 {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestBackupIgnoresOtherMessages(t *testing.T) {
	// Golden: restic backup --json -vv prints verbose_status messages.
	f := backupFixture(t, reply{stdout: "backup-verbose.stdout"})
	sum, err := f.build().Backup(context.Background(), request, nil)
	if err != nil || sum.SnapshotID != "381c03612feea0131acab0d6bb5676baddd78fde2bea2ceac80e0cbce8fc38fd" {
		t.Fatalf("summary = %+v, err = %v", sum, err)
	}
}
