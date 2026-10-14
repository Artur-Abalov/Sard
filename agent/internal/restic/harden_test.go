// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restic_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// A8c-2 (Р61, Р72, Р78): the edges of what restic restore and a restic that
// runs as root report.

func restoreOf(f *fixture) (restic.RestoreSummary, error) {
	return f.build().RestoreTo(context.Background(), restic.RestoreRequest{SnapshotID: fullID, Target: "/r"})
}

func TestTheSummaryOfResticSurvivesLinesThatComeAfterIt(t *testing.T) {
	f := newFixture(t, map[string]reply{"restore": {inline: `{"message_type":"summary","files_restored":2,"bytes_restored":2048}` + "\n" +
		`{"message_type":"status","percent_done":1}` + "\n" + "not json at all"}})
	got, err := restoreOf(f)
	if err != nil || got != (restic.RestoreSummary{Files: 2, Bytes: 2048}) {
		t.Fatalf("summary %+v, err %v", got, err)
	}
}

func TestAStatusLineIsNotASummary(t *testing.T) {
	f := newFixture(t, map[string]reply{"restore": {inline: `{"message_type":"status","files_restored":7,"bytes_restored":9}`}})
	got, err := restoreOf(f)
	if err != nil || got != (restic.RestoreSummary{}) {
		t.Fatalf("summary %+v, err %v", got, err)
	}
}

func TestAFailedRestoreWithoutAnyFileErrorIsTheExitOfRestic(t *testing.T) {
	f := newFixture(t, map[string]reply{"restore": {code: 1}})
	f.exec.fatal = "Fatal: something odd happened"
	_, err := restoreOf(f)
	var exit *restic.ExitError
	var incomplete *restic.RestoreError
	if !errors.As(err, &exit) || errors.As(err, &incomplete) {
		t.Fatalf("err = %v", err)
	}
}

func TestFileErrorsTogetherWithAFatalFailureAreTheFatalFailure(t *testing.T) {
	f := newFixture(t, map[string]reply{"restore": {code: 1}})
	f.exec.lines = append(itemLines(2), "Fatal: dial tcp 10.0.0.1:443: connect: connection refused")
	_, err := restoreOf(f)
	var incomplete *restic.RestoreError
	if !errors.Is(err, restic.ErrNetwork) || errors.As(err, &incomplete) {
		t.Fatalf("err = %v", err)
	}
}

func TestASuccessfulRestoreIsNoError(t *testing.T) {
	f := newFixture(t, map[string]reply{"restore": {}})
	f.exec.lines = itemLines(2) // warnings of a restic that exits 0
	if _, err := restoreOf(f); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestHasPathIsTrueWhateverFollowsTheNode(t *testing.T) {
	f := newFixture(t, map[string]reply{"ls": {inline: `{"message_type":"node","path":"/a"}` + "\n" + lsSnapshotLine}})
	found, err := f.build().HasPath(context.Background(), fullID, "/a")
	if err != nil || !found {
		t.Fatalf("found %v, err %v", found, err)
	}
}

func TestHasPathIsFalseWithTheErrorWhenResticFailsAfterANode(t *testing.T) {
	f := newFixture(t, map[string]reply{"ls": {inline: `{"message_type":"node","path":"/a"}`, code: 12}})
	found, err := f.build().HasPath(context.Background(), fullID, "/a")
	if found || !errors.Is(err, restic.ErrWrongPassword) {
		t.Fatalf("found %v, err %v", found, err)
	}
}

func TestCheckRootInputsRefusesAKindOfStorageWithoutAnAllowlist(t *testing.T) {
	f := newFixture(t, nil)
	f.root = true
	f.repo.URL = "rclone:remote:x"
	f.files["/etc/sard/main.pass"] = "pw\n"
	var unsupported *restic.BackendNotSupportedError
	if err := f.build().CheckRootInputs(context.Background()); !errors.As(err, &unsupported) || unsupported.Kind != "rclone" {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckRootInputsRefusesAForeignVariableOfTheEnvFile(t *testing.T) {
	f := newFixture(t, nil)
	err := f.asRoot("s3:https://s3.example.com/b", "HTTPS_PROXY=http://evil.example.com\n").CheckRootInputs(context.Background())
	if !errors.Is(err, restic.ErrInvalidEnvFile) || !strings.Contains(err.Error(), "HTTPS_PROXY") {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckRootInputsRefusesAKeyThatCannotBeRead(t *testing.T) {
	f := newFixture(t, nil)
	cli := f.asRoot("s3:https://s3.example.com/b", "AWS_ACCESS_KEY_ID=K\n")
	delete(f.files, "/etc/sard/main.pass")
	if err := cli.CheckRootInputs(context.Background()); !errors.Is(err, restic.ErrPasswordFile) {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckRootInputsAcceptsWhatARootRestoreNeeds(t *testing.T) {
	f := newFixture(t, nil)
	if err := f.asRoot("s3:https://s3.example.com/b", "AWS_ACCESS_KEY_ID=K\n").CheckRootInputs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.exec.calls) != 0 {
		t.Errorf("restic was started: %v", f.exec.calls)
	}
}

func TestALineOfTheEnvFileThatIsNoAssignmentIsToldSoWhenResticRunsAsRoot(t *testing.T) {
	f := newFixture(t, map[string]reply{"restore": {}})
	cli := f.asRoot("s3:https://s3.example.com/b", "AWS_ACCESS_KEY_ID=K\nnot an assignment\n")
	_, err := cli.RestoreTo(context.Background(), restic.RestoreRequest{SnapshotID: fullID, Staging: stagingFile(t)})
	if !errors.Is(err, restic.ErrInvalidEnvFile) || !strings.Contains(err.Error(), "want KEY=VALUE") || strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("err = %v", err)
	}
}

func TestAFailureOfTheKeyProviderIsItsOwnFailureAndStartsNoRestic(t *testing.T) {
	f := newFixture(t, map[string]reply{"restore": {}})
	boom := errors.New("the key store is locked")
	f.keys = fakeKeys{err: boom}
	f.root = true
	f.files["/etc/sard/main.pass"] = "pw\n"
	_, err := f.build().RestoreTo(context.Background(), restic.RestoreRequest{SnapshotID: fullID, Staging: stagingFile(t)})
	if !errors.Is(err, boom) || errors.Is(err, restic.ErrPasswordFile) || len(f.exec.calls) != 0 {
		t.Fatalf("err = %v, calls %d", err, len(f.exec.calls))
	}
}

// pipes are the pipes this process holds open.
func pipes(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if link, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name())); err == nil && strings.HasPrefix(link, "pipe:") {
			n++
		}
	}
	return n
}

func TestThePasswordPipeIsClosedWhenResticIsDone(t *testing.T) {
	f := newFixture(t, map[string]reply{"restore": {}})
	f.root = true
	f.files["/etc/sard/main.pass"] = "pw\n"
	staging := stagingFile(t)
	before := pipes(t)
	if _, err := f.build().RestoreTo(context.Background(), restic.RestoreRequest{SnapshotID: fullID, Staging: staging}); err != nil {
		t.Fatal(err)
	}
	if after := pipes(t); after != before {
		t.Errorf("%d pipes open before, %d after", before, after)
	}
}
