// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// A8c-2 (Р61, Р63): restic restore for repo restore, and restic ls to see
// whether a path is in a snapshot.

const fullID = "aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111"

func TestRestoreToRunsResticWithoutCacheAndLockAndRefusesToOverwriteByDefault(t *testing.T) {
	f := newFixture(t, map[string]reply{"restore": {}})
	_, err := f.build().RestoreTo(context.Background(), restic.RestoreRequest{SnapshotID: fullID, Target: "/r"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"restore", "--json", "--no-cache", "--no-lock", "--target", "/r", "--overwrite", "never", "--", fullID}
	if args := f.exec.call("restore").Args; !slices.Equal(args, want) {
		t.Errorf("args %q, want %q", args, want)
	}
}

func TestRestoreToPassesThePathsAndTheSFTPCommand(t *testing.T) {
	f := newFixture(t, map[string]reply{"restore": {}})
	_, err := f.build().RestoreTo(context.Background(), restic.RestoreRequest{
		SnapshotID: fullID, Target: "/r", Include: []string{"/srv/a", "/srv/b"}, SFTPCommand: "setpriv ssh",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"restore", "--json", "--no-cache", "--no-lock", "--target", "/r", "--overwrite", "never",
		"--include", "/srv/a", "--include", "/srv/b", "-o", "sftp.command=setpriv ssh", "--", fullID}
	if args := f.exec.call("restore").Args; !slices.Equal(args, want) {
		t.Errorf("args %q, want %q", args, want)
	}
}

func TestRestoreToReturnsTheSummaryOfRestic(t *testing.T) {
	f := newFixture(t, map[string]reply{"restore": {inline: `{"message_type":"status","percent_done":0.5}` + "\n" +
		`{"message_type":"summary","total_files":3,"files_restored":2,"total_bytes":9000,"bytes_restored":2048}`}})
	got, err := f.build().RestoreTo(context.Background(), restic.RestoreRequest{SnapshotID: fullID, Target: "/r"})
	if err != nil || got != (restic.RestoreSummary{Files: 2, Bytes: 2048}) {
		t.Fatalf("summary %+v, err %v", got, err)
	}
}

func itemLines(n int) []string {
	var lines []string
	for i := range n {
		lines = append(lines, fmt.Sprintf(`{"message_type":"error","error":{"message":"lchown: operation not permitted"},"during":"restore","item":"/srv/f%d"}`, i))
	}
	return lines
}

func TestRestoreToReportsTheFilesResticCouldNotRestore(t *testing.T) {
	f := newFixture(t, map[string]reply{"restore": {code: 1}})
	f.exec.lines = append(itemLines(12), itemLines(1)[0], `{"message_type":"exit_error","code":1,"message":"Fatal: There were 13 errors"}`)
	_, err := f.build().RestoreTo(context.Background(), restic.RestoreRequest{SnapshotID: fullID, Target: "/r"})
	var incomplete *restic.RestoreError
	if !errors.As(err, &incomplete) {
		t.Fatalf("err = %v", err)
	}
	paths := incomplete.Paths()
	if len(paths) != 12 || paths[0] != "/srv/f0" || paths[11] != "/srv/f11" {
		t.Errorf("paths %v: each path counts once, in the order of the first report", paths)
	}
	if !strings.Contains(incomplete.Error(), "12") {
		t.Errorf("text %q", incomplete.Error())
	}
	if errors.Is(err, restic.ErrNoSpace) {
		t.Error("lchown is not a lack of space")
	}
}

func TestRestoreToTellsALackOfSpaceFromTheItemsAndFromTheFatalMessage(t *testing.T) {
	item := `{"message_type":"error","error":{"message":"write /r/f: no space left on device"},"during":"restore","item":"/f"}`
	for name, lines := range map[string][]string{
		"item":  {item, `{"message_type":"exit_error","code":1,"message":"Fatal: There were 1 errors"}`},
		"fatal": {"Fatal: write /r/f: no space left on device"},
	} {
		f := newFixture(t, map[string]reply{"restore": {code: 1}})
		f.exec.lines = lines
		_, err := f.build().RestoreTo(context.Background(), restic.RestoreRequest{SnapshotID: fullID, Target: "/r"})
		if !errors.Is(err, restic.ErrNoSpace) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestRestoreToLeavesAFailureOfTheStorageToTheUsualErrors(t *testing.T) {
	f := newFixture(t, map[string]reply{"restore": {code: 1}})
	f.exec.fatal = "Fatal: dial tcp 10.0.0.1:443: connect: connection refused"
	_, err := f.build().RestoreTo(context.Background(), restic.RestoreRequest{SnapshotID: fullID, Target: "/r"})
	var incomplete *restic.RestoreError
	if !errors.Is(err, restic.ErrNetwork) || errors.As(err, &incomplete) {
		t.Fatalf("err = %v", err)
	}
}

func TestRestoreToStoppedByTheContextIsTheErrorOfTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	f := newFixture(t, map[string]reply{"restore": {code: 1, during: cancel}})
	f.exec.lines = itemLines(2)
	_, err := f.build().RestoreTo(ctx, restic.RestoreRequest{SnapshotID: fullID, Target: "/r"})
	var incomplete *restic.RestoreError
	if !errors.Is(err, context.Canceled) || errors.As(err, &incomplete) {
		t.Fatalf("err = %v", err)
	}
}

func TestRestoreToRequiresASnapshotAndATarget(t *testing.T) {
	f := newFixture(t, map[string]reply{})
	for _, req := range []restic.RestoreRequest{{Target: "/r"}, {SnapshotID: fullID}} {
		if _, err := f.build().RestoreTo(context.Background(), req); !errors.Is(err, restic.ErrInvalidRequest) {
			t.Errorf("%+v: err = %v", req, err)
		}
	}
}

const lsSnapshotLine = `{"time":"2026-10-10T06:00:00Z","paths":["/srv/data"],"id":"aaaa","message_type":"snapshot","struct_type":"snapshot"}`

func TestHasPathAsksResticToListThePathWithoutTheLock(t *testing.T) {
	f := newFixture(t, map[string]reply{"ls": {inline: lsSnapshotLine + "\n" + `{"name":"sub","type":"dir","path":"/srv/data/sub","message_type":"node","struct_type":"node"}`}})
	found, err := f.build().HasPath(context.Background(), fullID, "/srv/data/sub")
	if err != nil || !found {
		t.Fatalf("found %v, err %v", found, err)
	}
	want := []string{"ls", "--json", "--no-lock", "--", fullID, "/srv/data/sub"}
	if args := f.exec.call("ls").Args; !slices.Equal(args, want) {
		t.Errorf("args %q, want %q", args, want)
	}
}

func TestHasPathIsFalseWhenResticListsNoNode(t *testing.T) {
	f := newFixture(t, map[string]reply{"ls": {inline: lsSnapshotLine}})
	found, err := f.build().HasPath(context.Background(), fullID, "/srv/nothing")
	if err != nil || found {
		t.Fatalf("found %v, err %v", found, err)
	}
}

func TestHasPathReportsTheExitCodeOfRestic(t *testing.T) {
	f := newFixture(t, map[string]reply{"ls": {code: 12}})
	if _, err := f.build().HasPath(context.Background(), fullID, "/x"); !errors.Is(err, restic.ErrWrongPassword) {
		t.Fatalf("err = %v", err)
	}
}

// A8c-2 (Р73): restic is given the staging directory by descriptor 3 and
// writes into its subdirectory root, never into a path of the target.
func TestRestoreToWritesIntoTheRootDirectoryOfAnInheritedStagingDirectory(t *testing.T) {
	staging, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = staging.Close() }()
	f := newFixture(t, map[string]reply{"restore": {}})
	f.files["/etc/sard/main.pass"], f.root = "pw\n", true
	if _, err := f.build().RestoreTo(context.Background(), restic.RestoreRequest{SnapshotID: fullID, Staging: staging}); err != nil {
		t.Fatal(err)
	}
	c := f.exec.call("restore")
	want := []string{"restore", "--password-file", "/proc/self/fd/4", "--json", "--no-cache", "--no-lock", "--target", "/proc/self/fd/3/root", "--overwrite", "never", "--", fullID}
	if !slices.Equal(c.Args, want) {
		t.Errorf("args %q, want %q", c.Args, want)
	}
	if len(c.ExtraFiles) != 2 || c.ExtraFiles[0] != staging {
		t.Errorf("inherited %v: the staging directory is descriptor 3, the password pipe 4", c.ExtraFiles)
	}
}

// Р76: a quota is a lack of space; a read-only file system is its own error.
func TestRestoreToTellsAQuotaAndAReadOnlyFileSystemFromOtherFailures(t *testing.T) {
	for cause, want := range map[string]error{
		"write /r/f: disk quota exceeded":   restic.ErrNoSpace,
		"mkdir /r/d: read-only file system": restic.ErrReadOnly,
	} {
		item := fmt.Sprintf(`{"message_type":"error","error":{"message":%q},"during":"restore","item":"/f"}`, cause)
		for name, lines := range map[string][]string{
			"item":  {item, `{"message_type":"exit_error","code":1,"message":"Fatal: There were 1 errors"}`},
			"fatal": {"Fatal: " + cause},
		} {
			f := newFixture(t, map[string]reply{"restore": {code: 1}})
			f.exec.lines = lines
			_, err := f.build().RestoreTo(context.Background(), restic.RestoreRequest{SnapshotID: fullID, Target: "/r"})
			if !errors.Is(err, want) {
				t.Errorf("%s, %s: err = %v, want %v", cause, name, err, want)
			}
		}
	}
}

func TestARootRestoreWithoutAStagingDirectoryIsRefused(t *testing.T) {
	f := newFixture(t, map[string]reply{"restore": {}})
	f.root = true
	f.files["/etc/sard/main.pass"] = "pw\n"
	_, err := f.build().RestoreTo(context.Background(), restic.RestoreRequest{SnapshotID: fullID, Target: "/r"})
	if !errors.Is(err, restic.ErrInvalidRequest) || len(f.exec.calls) != 0 {
		t.Fatalf("err = %v, calls %d", err, len(f.exec.calls))
	}
}

// R2a: the progress of the restore of the console is what restic reports in its status lines.
func TestRestoreToReportsTheProgressOfRestic(t *testing.T) {
	f := newFixture(t, map[string]reply{"restore": {inline: `{"message_type":"status","percent_done":0.5,"total_files":2,"files_restored":1,"total_bytes":200,"bytes_restored":100}` + "\n" +
		`{"message_type":"summary","total_files":2,"files_restored":2,"total_bytes":200,"bytes_restored":200}`}})
	var got []restic.Progress
	_, err := f.build().RestoreTo(context.Background(), restic.RestoreRequest{SnapshotID: fullID, Target: "/r", Progress: func(p restic.Progress) { got = append(got, p) }})
	if err != nil {
		t.Fatal(err)
	}
	want := []restic.Progress{{FilesDone: 1, TotalFiles: 2, BytesDone: 100, TotalBytes: 200, PercentDone: 0.5}}
	if !slices.Equal(got, want) {
		t.Errorf("progress %+v, want %+v", got, want)
	}
}
