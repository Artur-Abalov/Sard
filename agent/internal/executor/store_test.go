// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package executor_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/executor"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

func fileOf(f *fixture, kind, id string) string {
	sum := sha256.Sum256([]byte(id))
	return filepath.Join(f.opts.StateDir, kind, hex.EncodeToString(sum[:])+".json")
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

// runOnce submits id, lets the handler succeed and returns the result.
func runOnce(t *testing.T, f *fixture, id string) *agentv1.StepResult {
	t.Helper()
	f.e.Submit(backup(id))
	accepted(t, f.sink, id)
	f.files.next(t).finish(snapshot("snap-"+id), nil)
	return f.sink.result(t)
}

func pendingIDs(e *executor.Executor) string {
	var ids []string
	for _, r := range e.PendingResults() {
		ids = append(ids, r.GetCommandId())
	}
	return strings.Join(ids, ",")
}

// --- 2. a result survives a restart

// resent submits id again and checks the stored result comes back.
func resent(t *testing.T, f *fixture, id string) {
	t.Helper()
	f.e.Submit(backup(id))
	if r := f.sink.result(t); r.GetBackup().GetSnapshotId() != "snap-"+id {
		t.Fatalf("resent = %v", r)
	}
}

func wantFiles(t *testing.T, want map[string]bool) {
	t.Helper()
	for path, present := range want {
		if exists(path) != present {
			t.Errorf("%s exists = %v, want %v", path, !present, present)
		}
	}
}

func TestAnUnacknowledgedResultSurvivesARestartAndIsNeverRunAgain(t *testing.T) {
	f := setup(t, nil)
	first := runOnce(t, f, "c1")
	wantFiles(t, map[string]bool{fileOf(f, "results", "c1"): true})

	f.restart(t)
	pending := f.e.PendingResults()
	if len(pending) != 1 || !pending[0].GetFinishedAt().AsTime().Equal(first.GetFinishedAt().AsTime()) {
		t.Fatalf("PendingResults = %v, want %v", pending, first)
	}
	resent(t, f, "c1")

	if err := f.e.Ack("c1"); err != nil {
		t.Fatal(err)
	}
	wantFiles(t, map[string]bool{fileOf(f, "results", "c1"): false, fileOf(f, "acked", "c1"): true})
	if got := pendingIDs(f.e); got != "" {
		t.Fatalf("PendingResults after Ack = %s", got)
	}

	f.restart(t)
	resent(t, f, "c1") // acknowledged, still remembered: the same result again
	f.files.idle(t)
	if n := f.files.runs.Load(); n != 1 {
		t.Fatalf("handler ran %d times", n)
	}
}

func TestStateIsOwnerOnly(t *testing.T) {
	f := setup(t, nil)
	runOnce(t, f, "c1")
	if err := f.e.Ack("c1"); err != nil {
		t.Fatal(err)
	}
	runOnce(t, f, "c2")
	for _, path := range []string{f.opts.StateDir, filepath.Join(f.opts.StateDir, "results"), filepath.Join(f.opts.StateDir, "acked")} {
		if m := mode(t, path); m != 0o700 {
			t.Errorf("%s: %v", path, m)
		}
	}
	for _, path := range []string{fileOf(f, "results", "c2"), fileOf(f, "acked", "c1")} {
		if m := mode(t, path); m != 0o600 {
			t.Errorf("%s: %v", path, m)
		}
	}
}

func TestPendingResultsAreOrderedByFinishTime(t *testing.T) {
	f := setup(t, nil)
	runOnce(t, f, "b")
	f.clock.Advance(time.Second)
	runOnce(t, f, "a")
	f.e.Submit(backup("z"))
	accepted(t, f.sink, "z")
	f.files.next(t)
	f.e.Cancel("z") // finishes at the same instant as "a": ties go by command_id
	f.sink.result(t)
	f.restart(t)
	if got := pendingIDs(f.e); got != "b,a,z" {
		t.Fatalf("PendingResults = %s", got)
	}
}

func TestRejectedAndCancelledResultsArePersistedToo(t *testing.T) {
	f := setup(t, nil)
	s := backup("r1")
	s.Plugin = "tape"
	f.e.Submit(s)
	f.sink.result(t)
	f.restart(t)
	if got := pendingIDs(f.e); got != "r1" {
		t.Fatalf("PendingResults = %s", got)
	}
	f.e.Submit(s)
	wantResult(t, f.sink.result(t), "r1", rejected, `unknown plugin "tape"`)
}

func TestAcknowledgedCommandsAreForgottenAfterRetention(t *testing.T) {
	f := setup(t, func(o *executor.Options) { o.Retention = time.Hour })
	runOnce(t, f, "old")
	if err := f.e.Ack("old"); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(time.Hour - time.Nanosecond)
	f.restart(t)
	f.e.Submit(backup("old"))
	f.sink.result(t) // still remembered
	f.files.idle(t)

	f.clock.Advance(time.Nanosecond)
	runOnce(t, f, "new")
	if err := f.e.Ack("new"); err != nil { // an Ack also sweeps expired tombstones
		t.Fatal(err)
	}
	if exists(fileOf(f, "acked", "old")) {
		t.Fatal("an expired tombstone stays on disk")
	}
	f.e.Submit(backup("old")) // forgotten: a new command with a reused id runs
	accepted(t, f.sink, "old")
	f.files.next(t)
}

func TestExpiredTombstonesAreDroppedAtStart(t *testing.T) {
	f := setup(t, func(o *executor.Options) { o.Retention = time.Hour })
	runOnce(t, f, "old")
	if err := f.e.Ack("old"); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(time.Hour)
	f.restart(t)
	if exists(fileOf(f, "acked", "old")) {
		t.Fatal("an expired tombstone survives a restart")
	}
}

func TestAckIsIdempotentAndRefusesWhatHasNoResult(t *testing.T) {
	f := setup(t, nil)
	runOnce(t, f, "c1")
	for range 2 {
		if err := f.e.Ack("c1"); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.e.Ack("nope"); !errors.Is(err, executor.ErrNoResult) || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("Ack(unknown) = %v", err)
	}
	f.e.Submit(backup("c2"))
	accepted(t, f.sink, "c2")
	f.files.next(t)
	if err := f.e.Ack("c2"); !errors.Is(err, executor.ErrNoResult) {
		t.Fatalf("Ack(running) = %v", err)
	}
}

func TestAckedWinsOverResultsLeftByACrashBetweenTheTwoWrites(t *testing.T) {
	f := setup(t, nil)
	runOnce(t, f, "c1")
	results, _ := os.ReadFile(fileOf(f, "results", "c1"))
	if err := f.e.Ack("c1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileOf(f, "results", "c1"), results, 0o600); err != nil { // the crash left it behind
		t.Fatal(err)
	}
	f.restart(t)
	if got := pendingIDs(f.e); got != "" || exists(fileOf(f, "results", "c1")) {
		t.Fatalf("PendingResults = %q; leftover removed: %v", got, !exists(fileOf(f, "results", "c1")))
	}
}

func TestCorruptOrUnknownStateFilesAreKeptAndReported(t *testing.T) {
	f := setup(t, nil)
	runOnce(t, f, "c1")
	bad := fileOf(f, "results", "bad")
	future := fileOf(f, "results", "future")
	garbled := fileOf(f, "acked", "garbled")
	unreadable := fileOf(f, "results", "unreadable")
	files := map[string]string{bad: "{not json", future: `{"version":99,"result":{}}`, garbled: `{"version":1,"result":{"status":"NOPE"}}`}
	for path, body := range files {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(unreadable, 0o700); err != nil { // a directory where a file belongs
		t.Fatal(err)
	}
	leftover := filepath.Join(f.opts.StateDir, "results", ".tmp-123")
	if err := os.WriteFile(leftover, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.restart(t)
	if got := pendingIDs(f.e); got != "c1" {
		t.Fatalf("PendingResults = %s", got)
	}
	// Unreadable state is kept for inspection; a partial write is removed.
	wantFiles(t, map[string]bool{bad: true, future: true, garbled: true, unreadable: true, leftover: false})
	log := f.log.String()
	for _, path := range []string{bad, future, garbled, unreadable} {
		if !strings.Contains(log, filepath.Base(path)) {
			t.Errorf("%s is not reported; log:\n%s", filepath.Base(path), log)
		}
	}
}

func TestAResultThatCannotBeSavedIsStillReported(t *testing.T) {
	f := setup(t, nil)
	results := filepath.Join(f.opts.StateDir, "results")
	if err := os.RemoveAll(results); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(results, nil, 0o600); err != nil { // not a directory any more, even for root
		t.Fatal(err)
	}
	wantResult(t, runOnce(t, f, "c1"), "c1", succeeded, "")
	if !strings.Contains(f.log.String(), "cannot save the result") {
		t.Fatalf("log:\n%s", f.log)
	}
	if got := pendingIDs(f.e); got != "c1" {
		t.Fatalf("PendingResults = %s", got)
	}
	if err := f.e.Ack("c1"); err == nil {
		t.Fatal("Ack reports that the tombstone cannot be written")
	}
}

func TestAStateDirOpenToOthersIsRefused(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := executor.New(executor.Options{Handlers: registry{}, Sink: newSink(), StateDir: dir})
	if !errors.Is(err, executor.ErrInvalidOptions) || !strings.Contains(err.Error(), dir) {
		t.Fatalf("err = %v", err)
	}
}

func TestAStateDirThatCannotBeCreatedIsRefused(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := executor.New(executor.Options{Handlers: registry{}, Sink: newSink(), StateDir: filepath.Join(file, "state")})
	if !errors.Is(err, executor.ErrInvalidOptions) {
		t.Fatalf("err = %v", err)
	}
}

func TestAMissingStateDirIsCreatedOwnerOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state", "executor")
	if _, err := executor.New(executor.Options{Handlers: registry{}, Sink: newSink(), StateDir: dir}); err != nil {
		t.Fatal(err)
	}
	if m := mode(t, dir); m != 0o700 {
		t.Fatalf("mode = %v", m)
	}
}
