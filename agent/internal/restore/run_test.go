// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package restore_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
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
	req restic.RestoreRequest
	// writes are the files restic puts into the root directory of the
	// staging directory it was given, by relative path.
	writes  map[string]string
	staging string
	summary restic.RestoreSummary
	err     error
	// paths are the paths HasPath finds.
	paths   map[string]bool
	hasErr  error
	checked []string
}

func (f *fakeRestorer) RestoreTo(_ context.Context, req restic.RestoreRequest) (restic.RestoreSummary, error) {
	f.req = req
	for rel, data := range f.writes {
		path := filepath.Join(f.staging, "root", rel)
		_ = os.MkdirAll(filepath.Dir(path), 0o700)
		_ = os.WriteFile(path, []byte(data), 0o600)
	}
	return f.summary, f.err
}

// request is the restore of the snapshot into a target held and staged in a
// temporary directory; the restorer is told where the staging directory is.
func request(t *testing.T, r *fakeRestorer) (restore.Request, string) {
	t.Helper()
	dir := t.TempDir()
	_, stage, f := staged(t, newRecordingFS(), dir, names("x1y2"))
	if f != nil {
		t.Fatal(f)
	}
	r.staging = stage.Path()
	return restore.Request{Snapshot: theSnapshot(), Target: "/r", Staging: stage}, dir
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

func TestRestoreGivesResticTheFullIdTheStagingDirectoryAndThePaths(t *testing.T) {
	r := &fakeRestorer{summary: restic.RestoreSummary{Files: 2, Bytes: 2048}}
	req, _ := request(t, r)
	req.Paths, req.SFTPCommand = []string{"/srv/data/sub"}, "ssh"
	got, f := restore.Run(context.Background(), r, req, repoTarget())
	if f != nil || got != r.summary {
		t.Fatalf("got %+v, %v", got, f)
	}
	if r.req.SnapshotID != id("aaaa1111") || r.req.Staging != req.Staging.Dir().Handle() || r.req.Target != "" ||
		!slices.Equal(r.req.Include, []string{"/srv/data/sub"}) || r.req.SFTPCommand != "ssh" {
		t.Errorf("request %+v", r.req)
	}
}

// Р73: after a success what restic wrote moves into the target and the
// staging directory goes.
func TestWhatResticWroteIsMovedIntoTheTargetAndTheStagingDirectoryIsRemoved(t *testing.T) {
	r := &fakeRestorer{writes: map[string]string{"srv/data/a.txt": "A"}}
	req, dir := request(t, r)
	if _, f := restore.Run(context.Background(), r, req, repoTarget()); f != nil {
		t.Fatal(f)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "srv/data/a.txt")); err != nil || string(data) != "A" {
		t.Errorf("a.txt: %q, %v", data, err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".sard-restore-*")); len(left) != 0 {
		t.Errorf("staging left: %v", left)
	}
}

func TestAFailureOfResticRemovesTheStagingDirectoryWithWhatItHolds(t *testing.T) {
	r := &fakeRestorer{writes: map[string]string{"srv/partial": "x"}, err: fmt.Errorf("restic restore: %w", restic.ErrNetwork)}
	req, dir := request(t, r)
	if _, f := restore.Run(context.Background(), r, req, repoTarget()); f == nil {
		t.Fatal("no failure")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("left in the target: %v", entries)
	}
}

// Р61: when only some files could not be restored the rest stays.
func TestFilesResticCouldNotRestoreDoNotTakeBackWhatItRestored(t *testing.T) {
	r := &fakeRestorer{writes: map[string]string{"srv/ok": "x"}, err: &restic.RestoreError{Items: []restic.ItemError{{Item: "/srv/bad"}}}}
	req, dir := request(t, r)
	_, f := restore.Run(context.Background(), r, req, repoTarget())
	if f == nil || f.Reason != refusal.RestoreIncomplete {
		t.Fatalf("failure %v", f)
	}
	if _, err := os.Stat(filepath.Join(dir, "srv/ok")); err != nil {
		t.Errorf("what was restored is gone: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".sard-restore-*")); len(left) != 0 {
		t.Errorf("staging left: %v", left)
	}
}

func TestSomeFilesNotRestoredIsRestoreIncompleteWithTheFirstTenPaths(t *testing.T) {
	var items []restic.ItemError
	for i := range 12 {
		items = append(items, restic.ItemError{Item: fmt.Sprintf("/srv/f%02d", i)}, restic.ItemError{Item: fmt.Sprintf("/srv/f%02d", i)})
	}
	r := &fakeRestorer{err: fmt.Errorf("restic restore: %w", &restic.RestoreError{Items: items})}
	req, _ := request(t, r)
	_, f := restore.Run(context.Background(), r, req, repoTarget())
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
	req, _ := request(t, r)
	_, f := restore.Run(context.Background(), r, req, repoTarget())
	if !strings.Contains(f.Detail, `"/a", "/b"`) || strings.Contains(f.Detail, "first") {
		t.Errorf("detail %q", f.Detail)
	}
}

const partial = "partially restored"

func TestNoSpaceIsATargetWriteFailureThatNamesTheTargetAndThePartialResult(t *testing.T) {
	r := &fakeRestorer{err: fmt.Errorf("restic restore: %w", restic.ErrNoSpace)}
	req, _ := request(t, r)
	_, f := restore.Run(context.Background(), r, req, repoTarget())
	if f == nil || f.Reason != refusal.TargetWrite || f.Class != refusal.ClassWrite {
		t.Fatalf("failure %v", f)
	}
	for _, want := range []string{"/r", partial} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("%q is not in %q", want, f.Detail)
		}
	}
}

// Р76: a quota is told as a lack of space, a read-only file system as a
// target that cannot be written: both TARGET_WRITE.
func TestAReadOnlyTargetIsATargetWriteFailureLikeALackOfSpace(t *testing.T) {
	r := &fakeRestorer{err: fmt.Errorf("restic restore: %w", restic.ErrReadOnly)}
	req, _ := request(t, r)
	_, f := restore.Run(context.Background(), r, req, repoTarget())
	if f == nil || f.Reason != refusal.TargetWrite || f.Class != refusal.ClassWrite {
		t.Fatalf("failure %v", f)
	}
	for _, want := range []string{"read-only", "/r", partial} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("%q is not in %q", want, f.Detail)
		}
	}
}

func TestALostStorageIsUnavailableAndNamesThePartialResult(t *testing.T) {
	r := &fakeRestorer{err: fmt.Errorf("restic restore: %w", restic.ErrNetwork)}
	req, _ := request(t, r)
	_, f := restore.Run(context.Background(), r, req, repoTarget())
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
		req, _ := request(t, r)
		_, f := restore.Run(ctx, r, req, repoTarget())
		if f == nil || f.Reason != reason || !strings.Contains(f.Detail, partial) {
			t.Errorf("%s: failure %v", reason, f)
		}
	}
}

func TestOtherFailuresOfTheStorageAreToldAsTheOtherCommandsTellThem(t *testing.T) {
	r := &fakeRestorer{err: fmt.Errorf("restic restore: %w", restic.ErrWrongPassword)}
	req, _ := request(t, r)
	_, f := restore.Run(context.Background(), r, req, repoTarget())
	if f == nil || f.Reason != refusal.WrongPassword || strings.Contains(f.Detail, partial) {
		t.Fatalf("failure %v", f)
	}
}

func TestPathsOfAFailureHideTheSecrets(t *testing.T) {
	r := &fakeRestorer{err: &restic.RestoreError{Items: []restic.ItemError{{Item: "/srv/SECRET/file"}}}}
	req, _ := request(t, r)
	_, f := restore.Run(context.Background(), r, req, repoTarget())
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

// Р72, Р78: why restic may not run as root against a repository.
func TestAKindOfStorageWithoutAnAllowlistIsBackendNotSupported(t *testing.T) {
	f := restore.InputsFailure(&restic.BackendNotSupportedError{Kind: "rclone"})
	if f == nil || f.Reason != refusal.BackendNotSupported || f.Class != refusal.ClassUsage || !strings.Contains(f.Detail, "rclone") {
		t.Fatalf("failure %v", f)
	}
}

func TestAForeignVariableIsEnvFileInvalidAndNamesTheFileTheLineAndTheVariable(t *testing.T) {
	err := fmt.Errorf(`%w: env_file "/etc/sard/x.env": line 3: HTTPS_PROXY is not allowed`, restic.ErrInvalidEnvFile)
	f := restore.InputsFailure(err)
	if f == nil || f.Reason != refusal.EnvFileInvalid || f.Class != refusal.ClassUsage {
		t.Fatalf("failure %v", f)
	}
	for _, want := range []string{"/etc/sard/x.env", "line 3", "HTTPS_PROXY"} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("%q is not in %q", want, f.Detail)
		}
	}
	if strings.Contains(f.Detail, "invalid env_file:") {
		t.Errorf("the sentinel is repeated in %q", f.Detail)
	}
}

func TestAMissingPasswordFileIsPasswordFileMissingAndAnUnfitOneIsRejected(t *testing.T) {
	missing := restore.InputsFailure(fmt.Errorf("%w of repository %q: %w", restic.ErrPasswordFile, "base", fs.ErrNotExist))
	if missing == nil || missing.Reason != refusal.PasswordFileMissing {
		t.Errorf("missing: %v", missing)
	}
	unfit := restore.InputsFailure(fmt.Errorf("%w of repository %q: %w", restic.ErrPasswordFile, "base", errors.New("secret file x is not a regular file")))
	if unfit == nil || unfit.Reason != refusal.SecretFileRejected || !strings.Contains(unfit.Detail, "not a regular file") {
		t.Errorf("unfit: %v", unfit)
	}
}

func TestAnEnvFileThatIsGoneIsEnvFileMissingNotAMissingPassword(t *testing.T) {
	f := restore.InputsFailure(fmt.Errorf("env_file: %w", fs.ErrNotExist))
	if f == nil || f.Reason != refusal.EnvFileMissing {
		t.Fatalf("failure %v", f)
	}
}
