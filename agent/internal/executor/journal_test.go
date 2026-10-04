// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package executor_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/executor"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// interrupted is the D13 message the console and notifications show.
const interrupted = "interrupted: agent restarted before the step finished"

func pendingResult(t *testing.T, e *executor.Executor, id string) *agentv1.StepResult {
	t.Helper()
	for _, r := range e.PendingResults() {
		if r.GetCommandId() == id {
			return r
		}
	}
	t.Fatalf("no pending result for %s; pending: %s", id, pendingIDs(e))
	return nil
}

func writeState(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// breakDir replaces a state subdirectory by a file: every write into it fails, even for root.
func breakDir(t *testing.T, f *fixture, sub string) {
	t.Helper()
	dir := filepath.Join(f.opts.StateDir, sub)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	writeState(t, dir, "")
}

// interruptMidStep leaves c1 finished, c2 running (started a minute after
// it was accepted) and c3 queued, then kills the agent a minute later. It
// returns when c2 started and when the agent came back.
func interruptMidStep(t *testing.T, f *fixture) (started, restarted time.Time) {
	t.Helper()
	f.e.Submit(backup("c1"))
	accepted(t, f.sink, "c1")
	first := f.files.next(t)
	for _, id := range []string{"c2", "c3"} {
		f.e.Submit(backup(id))
		accepted(t, f.sink, id)
	}
	wantFiles(t, map[string]bool{fileOf(f, "journal", "c1"): true, fileOf(f, "journal", "c2"): true})
	f.clock.Advance(time.Minute)
	started = f.clock.Now()
	first.finish(snapshot("snap-c1"), nil)
	f.sink.result(t)
	f.files.next(t) // c2 runs; c3 waits: one step at a time
	f.sink.quiet(t)
	f.clock.Advance(time.Minute)
	f.restart(t) // no result for c2 or c3
	return started, f.clock.Now()
}

// resentAsInterrupted sends ids again, as the server does, and expects the stored failure.
func resentAsInterrupted(t *testing.T, f *fixture, ids ...string) {
	t.Helper()
	for _, id := range ids {
		f.e.Submit(backup(id))
		wantResult(t, f.sink.result(t), id, failed, interrupted)
	}
}

// Scenario: Перезапуск агента посреди шага.
func TestAStepInterruptedByARestartFailsOnceWithTheD13Message(t *testing.T) {
	f := setup(t, nil)
	started, restarted := interruptMidStep(t, f)
	if got := pendingIDs(f.e); got != "c1,c2,c3" {
		t.Fatalf("PendingResults = %s", got)
	}
	if ids := f.e.RunningIDs(); len(ids) != 0 {
		t.Fatalf("RunningIDs = %v", ids)
	}
	running := pendingResult(t, f.e, "c2")
	wantResult(t, running, "c2", failed, interrupted)
	if !running.GetStartedAt().AsTime().Equal(started) || !running.GetFinishedAt().AsTime().Equal(restarted) {
		t.Fatalf("c2 started %v, finished %v", running.GetStartedAt().AsTime(), running.GetFinishedAt().AsTime())
	}
	queued := pendingResult(t, f.e, "c3")
	wantResult(t, queued, "c3", failed, interrupted)
	if queued.GetStartedAt() != nil {
		t.Fatalf("a step that never started has started_at %v", queued.GetStartedAt().AsTime())
	}
	wantFiles(t, map[string]bool{
		fileOf(f, "journal", "c2"): false, fileOf(f, "results", "c2"): true,
		fileOf(f, "journal", "c3"): false, fileOf(f, "results", "c3"): true,
	})
}

// Scenario: Перезапуск агента посреди шага.
func TestAStepInterruptedByARestartNeverRunsAgain(t *testing.T) {
	f := setup(t, nil)
	interruptMidStep(t, f)
	resentAsInterrupted(t, f, "c2", "c3")
	f.restart(t)
	resentAsInterrupted(t, f, "c2", "c3")
	f.files.idle(t)
	if n := f.files.runs.Load(); n != 2 {
		t.Fatalf("handler ran %d times, want c1 and c2 once each", n)
	}
}

func TestAStepThatCrashedBeforeItsJournalEntryRunsOnceWhenSentAgain(t *testing.T) {
	f := setup(t, nil)
	partial := filepath.Join(f.opts.StateDir, "journal", ".tmp-42")
	writeState(t, partial, `{"version":1,"command_id":"c1"`) // the crash cut the write short
	f.restart(t)
	if got := pendingIDs(f.e); got != "" {
		t.Fatalf("PendingResults = %s", got)
	}
	wantFiles(t, map[string]bool{partial: false})
	runOnce(t, f, "c1")
	if n := f.files.runs.Load(); n != 1 {
		t.Fatalf("handler ran %d times", n)
	}
}

func TestAJournalEntryWrittenJustBeforeTheCrashBecomesAFailure(t *testing.T) {
	f := setup(t, nil)
	writeState(t, fileOf(f, "journal", "c1"), `{"version":1,"command_id":"c1","accepted_at":"2026-10-04T10:00:00Z"}`)
	f.restart(t)
	r := pendingResult(t, f.e, "c1")
	wantResult(t, r, "c1", failed, interrupted)
	if r.GetStartedAt() != nil {
		t.Fatalf("started_at = %v", r.GetStartedAt().AsTime())
	}
	f.e.Submit(backup("c1"))
	wantResult(t, f.sink.result(t), "c1", failed, interrupted)
	f.files.idle(t)
}

func TestAResultSavedJustBeforeTheCrashWinsOverItsJournalEntry(t *testing.T) {
	f := setup(t, nil)
	f.e.Submit(backup("c1"))
	accepted(t, f.sink, "c1")
	c := f.files.next(t)
	entry, err := os.ReadFile(fileOf(f, "journal", "c1"))
	if err != nil {
		t.Fatal(err)
	}
	c.finish(snapshot("snap-c1"), nil)
	f.sink.result(t)
	wantFiles(t, map[string]bool{fileOf(f, "journal", "c1"): false})
	writeState(t, fileOf(f, "journal", "c1"), string(entry)) // the crash came before the entry was removed

	f.restart(t)
	wantResult(t, pendingResult(t, f.e, "c1"), "c1", succeeded, "")
	wantFiles(t, map[string]bool{fileOf(f, "journal", "c1"): false})
	resent(t, f, "c1")
	if n := f.files.runs.Load(); n != 1 {
		t.Fatalf("handler ran %d times", n)
	}
}

func TestAnAcknowledgedCommandWinsOverItsJournalEntry(t *testing.T) {
	f := setup(t, nil)
	runOnce(t, f, "c1")
	if err := f.e.Ack("c1"); err != nil {
		t.Fatal(err)
	}
	writeState(t, fileOf(f, "journal", "c1"), `{"version":1,"command_id":"c1","accepted_at":"2026-10-04T10:00:00Z"}`)
	f.restart(t)
	if got := pendingIDs(f.e); got != "" {
		t.Fatalf("PendingResults = %s", got)
	}
	wantFiles(t, map[string]bool{fileOf(f, "journal", "c1"): false})
}

func TestAStateDirWithoutAJournalIsReadAndKeepsItsResults(t *testing.T) {
	f := setup(t, nil)
	if err := os.RemoveAll(filepath.Join(f.opts.StateDir, "journal")); err != nil { // the layout before the journal
		t.Fatal(err)
	}
	writeState(t, fileOf(f, "results", "old"),
		`{"version":1,"result":{"commandId":"old","status":"STEP_STATUS_SUCCEEDED","finishedAt":"2026-10-03T10:00:00Z"}}`)

	f.restart(t)
	wantResult(t, pendingResult(t, f.e, "old"), "old", succeeded, "")
	if m := mode(t, filepath.Join(f.opts.StateDir, "journal")); m != 0o700 {
		t.Fatalf("journal dir mode = %v", m)
	}
}

func TestJournalEntriesAreOwnerOnly(t *testing.T) {
	f := setup(t, nil)
	f.e.Submit(backup("c1"))
	accepted(t, f.sink, "c1")
	f.files.next(t)
	if m := mode(t, filepath.Join(f.opts.StateDir, "journal")); m != 0o700 {
		t.Errorf("journal dir: %v", m)
	}
	if m := mode(t, fileOf(f, "journal", "c1")); m != 0o600 {
		t.Errorf("journal entry: %v", m)
	}
}

func TestAJournalEntryHoldsNoStepConfig(t *testing.T) {
	f := setup(t, nil)
	s := backup("c1")
	s.ConfigJson = `{"paths":["/srv/very-private"]}`
	s.Tags = map[string]string{"owner": "tenant-x"}
	f.e.Submit(s)
	accepted(t, f.sink, "c1")
	f.files.next(t)
	entry, err := os.ReadFile(fileOf(f, "journal", "c1"))
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"very-private", "tenant-x", "main", "files"} {
		if strings.Contains(string(entry), leak) {
			t.Errorf("journal entry holds %q: %s", leak, entry)
		}
	}
}

func TestACommandThatCannotBeJournaledIsRejectedWithoutRunning(t *testing.T) {
	f := setup(t, nil)
	breakDir(t, f, "journal")
	f.e.Submit(backup("c1"))
	r := f.sink.result(t)
	if r.GetStatus() != rejected || !strings.HasPrefix(r.GetMessage(), "cannot record the command: ") {
		t.Fatalf("result = %v", r)
	}
	f.files.idle(t)
}

func TestAJournalThatBreaksAfterAcceptLetsTheStepsRunAndIsReported(t *testing.T) {
	f := setup(t, nil)
	f.e.Submit(backup("c1"))
	accepted(t, f.sink, "c1")
	first := f.files.next(t)
	f.e.Submit(backup("c2"))
	accepted(t, f.sink, "c2")
	breakDir(t, f, "journal")

	first.finish(snapshot("snap-c1"), nil)
	wantResult(t, f.sink.result(t), "c1", succeeded, "")
	f.files.next(t) // c2 starts although its start cannot be recorded
	log := f.log.String()
	for _, want := range []string{"cannot remove the journal entry", "cannot record the start of the step"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

func TestAnInterruptedStepWhoseFailureCannotBeSavedIsStillReported(t *testing.T) {
	f := setup(t, nil)
	f.e.Submit(backup("c1"))
	accepted(t, f.sink, "c1")
	f.files.next(t)
	if err := os.Mkdir(fileOf(f, "results", "c1"), 0o700); err != nil { // a directory where the result belongs
		t.Fatal(err)
	}

	f.restart(t)
	wantResult(t, pendingResult(t, f.e, "c1"), "c1", failed, interrupted)
	wantFiles(t, map[string]bool{fileOf(f, "journal", "c1"): true}) // the next start tries again
	if !strings.Contains(f.log.String(), "cannot save the result") {
		t.Fatalf("log:\n%s", f.log)
	}
}

func TestCorruptOrUnknownJournalEntriesAreKeptAndReported(t *testing.T) {
	f := setup(t, nil)
	bad := fileOf(f, "journal", "bad")
	future := fileOf(f, "journal", "future")
	writeState(t, bad, "{not json")
	writeState(t, future, `{"version":2,"command_id":"future"}`)
	f.restart(t)
	if got := pendingIDs(f.e); got != "" {
		t.Fatalf("PendingResults = %s", got)
	}
	wantFiles(t, map[string]bool{bad: true, future: true})
	for _, path := range []string{bad, future} {
		if !strings.Contains(f.log.String(), filepath.Base(path)) {
			t.Errorf("%s is not reported; log:\n%s", filepath.Base(path), f.log)
		}
	}
}

func TestAnUnreadableJournalDirStopsTheStart(t *testing.T) {
	f := setup(t, nil)
	breakDir(t, f, "journal")
	if _, err := executor.New(f.opts); err == nil {
		t.Fatal("New reads a journal that is not a directory")
	}
}

// idsInDirOrder returns two ids whose state files sort first-then-second,
// the order a directory is read in.
func idsInDirOrder(f *fixture) (first, second string) {
	first, second = "a", "b"
	if filepath.Base(fileOf(f, "journal", first)) > filepath.Base(fileOf(f, "journal", second)) {
		return second, first
	}
	return first, second
}

func TestAJournalLeftoverDoesNotHideTheInterruptedStepsAfterIt(t *testing.T) {
	f := setup(t, nil)
	leftover, cut := idsInDirOrder(f)
	runOnce(t, f, leftover)
	for _, id := range []string{leftover, cut} {
		writeState(t, fileOf(f, "journal", id), `{"version":1,"command_id":"`+id+`","accepted_at":"2026-10-04T10:00:00Z"}`)
	}
	f.restart(t)
	wantResult(t, pendingResult(t, f.e, leftover), leftover, succeeded, "")
	wantResult(t, pendingResult(t, f.e, cut), cut, failed, interrupted)
}

func TestALeftoverThatCannotBeRemovedIsReported(t *testing.T) {
	f := setup(t, nil)
	runOnce(t, f, "c1")
	if err := f.e.Ack("c1"); err != nil {
		t.Fatal(err)
	}
	stuck := fileOf(f, "results", "c1") // a non-empty directory where the leftover result was
	if err := os.MkdirAll(filepath.Join(stuck, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	f.restart(t)
	if !strings.Contains(f.log.String(), "cannot remove an acknowledged result") {
		t.Fatalf("log:\n%s", f.log)
	}
}

func TestAnUnreadableJournalEntryIsReportedWithItsOwnError(t *testing.T) {
	f := setup(t, nil)
	writeState(t, fileOf(f, "journal", "bad"), "{not json")
	if err := os.Mkdir(fileOf(f, "journal", "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	f.restart(t)
	for _, want := range []string{"invalid character", "is a directory"} {
		if !strings.Contains(f.log.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, f.log)
		}
	}
}
