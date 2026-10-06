// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
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

		OneFileSystem: true,
	}
	if _, err := f.build().Backup(context.Background(), req, nil); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"backup", "--json", "--retry-lock", "5m",
		"--tag", "sard", "--tag", "job:nightly",
		"--one-file-system",
		"--exclude", "*.tmp", "--exclude", "/srv/data/cache",
		"--", "/srv/data", "-odd-name",
	}
	if got := f.exec.call("backup").Args; !slices.Equal(got, want) {
		t.Fatalf("args = %q", got)
	}
}

// A6b Ф2: restic crosses file systems unless the request forbids it.
func TestBackupCrossesFileSystemsByDefault(t *testing.T) {
	f := backupFixture(t, reply{stdout: "backup-ok.stdout"})
	if _, err := f.build().Backup(context.Background(), request, nil); err != nil {
		t.Fatal(err)
	}
	if got := f.exec.call("backup").Args; slices.Contains(got, "--one-file-system") {
		t.Fatalf("args = %q", got)
	}
}

// A6b Ф12: once restic has exited 0 with a summary the snapshot exists, even
// if the step was cancelled a moment before the wrapper looked.
func TestBackupCancelledAfterResticSavedTheSnapshotKeepsTheSummary(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := backupFixture(t, reply{stdout: "backup-ok.stdout", during: cancel})
	sum, err := f.build().Backup(ctx, request, nil)
	if err != nil || sum.SnapshotID == "" {
		t.Fatalf("summary = %+v, err = %v", sum, err)
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
	// A6b Ф1: the path that failed at scan and at archival counts once.
	if want := `restic backup: at least one source file could not be read: unreadable paths (2): "/tmp/sardcap2/data/locked", "/tmp/sardcap2/data/sub/unreadable.txt"`; err.Error() != want {
		t.Errorf("text = %q", err.Error())
	}
	if want := `msg="restic exited" command=backup exit_code=3`; !strings.Contains(f.log.String(), want) {
		t.Errorf("log lacks %q:\n%s", want, f.log.String())
	}
}

func TestPartialErrorNamesTheFirstTenDistinctPathsAndTheirNumber(t *testing.T) {
	var items []restic.ItemError
	for i := 1; i <= 11; i++ {
		p := fmt.Sprintf("/d/f%d", i)
		items = append(items, restic.ItemError{Item: p, During: "scan"}, restic.ItemError{Item: p, During: "archival"})
	}
	got := (&restic.PartialError{Items: items}).Error()
	if !strings.HasPrefix(got, `at least one source file could not be read: unreadable paths (11), first 10: "/d/f1", "/d/f2"`) ||
		!strings.HasSuffix(got, `"/d/f10"`) || strings.Contains(got, "f11") {
		t.Errorf("text = %q", got)
	}
}

func TestPartialErrorTextIsOneLineWhateverTheFileName(t *testing.T) {
	got := (&restic.PartialError{Items: []restic.ItemError{{Item: "/d/a\nb"}}}).Error()
	if strings.ContainsAny(got, "\n") || !strings.Contains(got, `"/d/a\nb"`) {
		t.Errorf("text = %q", got)
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

// Golden (restic 0.19.1): the only path is gone; restic names it in a plain
// line, and the error keeps the name (A6b Ф15).
func TestBackupFailure(t *testing.T) {
	f := backupFixture(t, reply{stderr: "backup-missing.stderr", code: 1})
	_, err := f.build().Backup(context.Background(), request, nil)
	if err == nil || err.Error() != `restic backup: exit code 1: Fatal: all source directories/files do not exist: "/tmp/sardcap2/nonexistent"` {
		t.Fatalf("err = %v", err)
	}
}

// Golden (restic 0.19.1): one of two paths is gone. restic saves the other
// one and exits 3 without an error item, only a plain line naming the path.
func TestBackupWithAPathThatVanishedIsPartialAndNamesThePath(t *testing.T) {
	f := backupFixture(t, reply{stdout: "backup-vanished.stdout", stderr: "backup-vanished.stderr", code: 3})
	sum, err := f.build().Backup(context.Background(), request, nil)
	var partial *restic.PartialError
	if sum.SnapshotID == "" || !errors.As(err, &partial) || len(partial.Items) != 1 || partial.Items[0].Item != "/srv/gone" {
		t.Fatalf("summary = %+v, err = %v", sum, err)
	}
	if !strings.Contains(err.Error(), `unreadable paths (1): "/srv/gone"`) {
		t.Errorf("text = %q", err)
	}
}

// Golden (restic 0.19.1): restic's fatal message has a line break in it; the
// error text is one line and keeps the pattern (A6b Ф17).
func TestBackupWithAnExcludePatternResticRefusesFailsWithOneLine(t *testing.T) {
	f := backupFixture(t, reply{stderr: "backup-bad-exclude.stderr", code: 1})
	_, err := f.build().Backup(context.Background(), restic.BackupRequest{Paths: []string{"/srv"}, Excludes: []string{"["}}, nil)
	if err == nil || err.Error() != "restic backup: exit code 1: Fatal: --exclude: invalid pattern(s) provided: [" {
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

// Ten distinct paths are all named; "first ten" is only for more.
func TestPartialErrorWithTenPathsNamesThemAllWithoutFirstTen(t *testing.T) {
	var items []restic.ItemError
	for i := 1; i <= 10; i++ {
		items = append(items, restic.ItemError{Item: fmt.Sprintf("/d/f%d", i)})
	}
	got := (&restic.PartialError{Items: items}).Error()
	if !strings.Contains(got, "unreadable paths (10): ") || strings.Contains(got, "first") || !strings.HasSuffix(got, `"/d/f10"`) {
		t.Errorf("text = %q", got)
	}
}

// Restic exits 1 having only said that a path is gone, without a fatal
// message: the error is the exit code, no half-sentence with a path list.
func TestBackupFailureWithoutAFatalMessageDoesNotListPaths(t *testing.T) {
	f := backupFixture(t, reply{stderr: "backup-gone-only.stderr", code: 1})
	_, err := f.build().Backup(context.Background(), request, nil)
	if err == nil || err.Error() != "restic backup: exit code 1" {
		t.Fatalf("err = %v", err)
	}
}

// A process that could not be waited for, while the step was cancelled, is
// a cancellation, not the failure of the wait.
func TestBackupCancelledWhileResticFailsToReportItsExitIsACancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := backupFixture(t, reply{code: 0, err: errors.New("wait failed"), during: cancel})
	if _, err := f.build().Backup(ctx, request, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}
