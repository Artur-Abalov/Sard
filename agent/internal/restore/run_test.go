// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restore_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/restore"
)

// Rules Р61, Р63, Р64 (A8c-2): the restore itself and its failures.

type fakeRestorer struct {
	req     restic.RestoreRequest
	summary restic.RestoreSummary
	err     error
	// paths are the paths HasPath finds.
	paths   map[string]bool
	hasErr  error
	checked []string
}

func (f *fakeRestorer) RestoreTo(_ context.Context, req restic.RestoreRequest) (restic.RestoreSummary, error) {
	f.req = req
	return f.summary, f.err
}

func (f *fakeRestorer) HasPath(_ context.Context, _, path string) (bool, error) {
	f.checked = append(f.checked, path)
	return f.paths[path], f.hasErr
}

func repoTarget() repoinit.Target {
	return repoinit.Target{Name: "base", Backend: "local", Scrub: repoinit.Scrubber("", nil, "SECRET")}
}

func theSnapshot() restore.Snapshot {
	return restore.Snapshot{ID: id("aaaa1111"), ShortID: "aaaa1111", Paths: []string{"/srv/data"}}
}

func TestRestoreGivesResticTheFullIdTheTargetAndWhatToReplace(t *testing.T) {
	r := &fakeRestorer{summary: restic.RestoreSummary{Files: 2, Bytes: 2048}}
	got, f := restore.Run(context.Background(), r, restore.Request{
		Snapshot: theSnapshot(), Target: "/r", Overwrite: true, Paths: []string{"/srv/data/sub"}, SFTPCommand: "ssh",
	}, repoTarget())
	if f != nil || got != r.summary {
		t.Fatalf("got %+v, %v", got, f)
	}
	want := restic.RestoreRequest{SnapshotID: id("aaaa1111"), Target: "/r", Overwrite: true, Include: []string{"/srv/data/sub"}, SFTPCommand: "ssh"}
	if fmt.Sprint(r.req) != fmt.Sprint(want) {
		t.Errorf("request %+v, want %+v", r.req, want)
	}
}

func TestSomeFilesNotRestoredIsRestoreIncompleteWithTheFirstTenPaths(t *testing.T) {
	var items []restic.ItemError
	for i := range 12 {
		items = append(items, restic.ItemError{Item: fmt.Sprintf("/srv/f%02d", i)}, restic.ItemError{Item: fmt.Sprintf("/srv/f%02d", i)})
	}
	r := &fakeRestorer{err: fmt.Errorf("restic restore: %w", &restic.RestoreError{Items: items})}
	_, f := restore.Run(context.Background(), r, restore.Request{Snapshot: theSnapshot(), Target: "/r"}, repoTarget())
	if f == nil || f.Reason != refusal.RestoreIncomplete || f.Class != refusal.ClassAgentError {
		t.Fatalf("failure %v", f)
	}
	for _, want := range []string{"12", `"/srv/f00"`, `"/srv/f09"`, "/r"} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("%q is not in %q", want, f.Detail)
		}
	}
	if strings.Contains(f.Detail, "/srv/f10") {
		t.Errorf("more than ten paths in %q", f.Detail)
	}
}

func TestAFewFilesNotRestoredAreAllNamed(t *testing.T) {
	r := &fakeRestorer{err: &restic.RestoreError{Items: []restic.ItemError{{Item: "/a"}, {Item: "/b"}}}}
	_, f := restore.Run(context.Background(), r, restore.Request{Snapshot: theSnapshot(), Target: "/r"}, repoTarget())
	if !strings.Contains(f.Detail, `"/a", "/b"`) || strings.Contains(f.Detail, "first") {
		t.Errorf("detail %q", f.Detail)
	}
}

const partial = "partially restored"

func TestNoSpaceIsATargetWriteFailureThatNamesTheTargetAndThePartialResult(t *testing.T) {
	r := &fakeRestorer{err: fmt.Errorf("restic restore: %w", restic.ErrNoSpace)}
	_, f := restore.Run(context.Background(), r, restore.Request{Snapshot: theSnapshot(), Target: "/r"}, repoTarget())
	if f == nil || f.Reason != refusal.TargetWrite || f.Class != refusal.ClassWrite {
		t.Fatalf("failure %v", f)
	}
	for _, want := range []string{"/r", partial} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("%q is not in %q", want, f.Detail)
		}
	}
}

func TestALostStorageIsUnavailableAndNamesThePartialResult(t *testing.T) {
	r := &fakeRestorer{err: fmt.Errorf("restic restore: %w", restic.ErrNetwork)}
	_, f := restore.Run(context.Background(), r, restore.Request{Snapshot: theSnapshot(), Target: "/r"}, repoTarget())
	if f == nil || f.Reason != refusal.BackendUnavailable || !strings.Contains(f.Detail, partial) {
		t.Fatalf("failure %v", f)
	}
}

func TestAnInterruptAndATimeoutNameThePartialResult(t *testing.T) {
	interrupted, cancel := context.WithCancel(context.Background())
	cancel()
	timedOut, cancelTimeout := context.WithCancelCause(context.Background())
	cancelTimeout(repoinit.ErrTimeout)
	for ctx, reason := range map[context.Context]refusal.Reason{interrupted: refusal.Interrupted, timedOut: refusal.Timeout} {
		r := &fakeRestorer{err: fmt.Errorf("restic restore: %w", ctx.Err())}
		_, f := restore.Run(ctx, r, restore.Request{Snapshot: theSnapshot(), Target: "/r"}, repoTarget())
		if f == nil || f.Reason != reason || !strings.Contains(f.Detail, partial) {
			t.Errorf("%s: failure %v", reason, f)
		}
	}
}

func TestOtherFailuresOfTheStorageAreToldAsTheOtherCommandsTellThem(t *testing.T) {
	r := &fakeRestorer{err: fmt.Errorf("restic restore: %w", restic.ErrWrongPassword)}
	_, f := restore.Run(context.Background(), r, restore.Request{Snapshot: theSnapshot(), Target: "/r"}, repoTarget())
	if f == nil || f.Reason != refusal.WrongPassword || strings.Contains(f.Detail, partial) {
		t.Fatalf("failure %v", f)
	}
}

func TestPathsOfAFailureHideTheSecrets(t *testing.T) {
	r := &fakeRestorer{err: &restic.RestoreError{Items: []restic.ItemError{{Item: "/srv/SECRET/file"}}}}
	_, f := restore.Run(context.Background(), r, restore.Request{Snapshot: theSnapshot(), Target: "/r"}, repoTarget())
	if strings.Contains(f.Detail, "SECRET") || !strings.Contains(f.Detail, "[REDACTED]") {
		t.Errorf("detail %q", f.Detail)
	}
}

func TestPathsOfTheSnapshotAreFoundBeforeAnythingIsWritten(t *testing.T) {
	r := &fakeRestorer{paths: map[string]bool{"/srv/data/a": true, "/srv/data/b": true}}
	if f := restore.RequirePaths(context.Background(), r, theSnapshot(), []string{"/srv/data/a", "/srv/data/b"}, repoTarget()); f != nil {
		t.Fatal(f)
	}
}

func TestAPathThatIsNotInTheSnapshotIsNamed(t *testing.T) {
	r := &fakeRestorer{paths: map[string]bool{"/srv/data/a": true}}
	f := restore.RequirePaths(context.Background(), r, theSnapshot(), []string{"/srv/data/a", "/srv/nothing", "/srv/other"}, repoTarget())
	if f == nil || f.Reason != refusal.PathNotInSnapshot || f.Class != refusal.ClassUsage || !strings.Contains(f.Detail, "/srv/nothing") {
		t.Fatalf("failure %v", f)
	}
	if len(r.checked) != 2 {
		t.Errorf("checked %v: the first missing path ends the check", r.checked)
	}
}

func TestAFailureToLookIntoTheSnapshotIsToldAsAFailureOfRestic(t *testing.T) {
	r := &fakeRestorer{hasErr: restic.ErrWrongPassword}
	f := restore.RequirePaths(context.Background(), r, theSnapshot(), []string{"/srv/data"}, repoTarget())
	if f == nil || f.Reason != refusal.WrongPassword {
		t.Fatalf("failure %v", f)
	}
}

func TestTheSFTPCommandStartsSSHAsTheServiceUser(t *testing.T) {
	as := restic.RunAs{UID: 990, GID: 991}
	for _, c := range []struct {
		address hostsetup.SFTPAddress
		want    string
	}{
		{hostsetup.SFTPAddress{User: "backup", Host: "nas.example.com", Port: 2222}, "setpriv --reuid=990 --regid=991 --clear-groups ssh -p 2222 -l backup -s -- nas.example.com sftp"},
		{hostsetup.SFTPAddress{Host: "::1", Port: 22}, "setpriv --reuid=990 --regid=991 --clear-groups ssh -p 22 -s -- ::1 sftp"},
	} {
		if got := restore.SFTPCommand(as, c.address); got != c.want {
			t.Errorf("command %q, want %q", got, c.want)
		}
	}
}
