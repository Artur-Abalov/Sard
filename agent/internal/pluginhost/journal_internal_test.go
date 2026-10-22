// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/restorejournal"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// removeFailsFS fails the removal of a file.
type removeFailsFS struct {
	hostsetup.FS
	err error
}

func (f removeFailsFS) Remove(path string) error {
	if f.err != nil {
		return f.err
	}
	return f.FS.Remove(path)
}

func journalOver(t *testing.T, fsys hostsetup.FS, log *slog.Logger) (*Journal, *restorejournal.Store) {
	t.Helper()
	store := restorejournal.New(restorejournal.Options{
		Dir: filepath.Join(t.TempDir(), "restore-journal"), FS: fsys, Owner: hostsetup.Attrs{UID: os.Getuid(), GID: os.Getgid()},
	})
	return NewJournal(store, log), store
}

func hasEntry(t *testing.T, store *restorejournal.Store, id string) bool {
	t.Helper()
	records, _, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range records {
		if r.CommandID == id {
			return true
		}
	}
	return false
}

func result(id string, status agentv1.StepStatus) *agentv1.StepResult {
	return &agentv1.StepResult{CommandId: id, Status: status}
}

func TestAJournalWithoutALogRemovesAndSurvivesAFailedRemoval(t *testing.T) {
	fsys := &removeFailsFS{FS: hostsetup.OS{}}
	j, store := journalOver(t, fsys, nil)
	if err := j.save("p", "c1", []byte("e1")); err != nil {
		t.Fatal(err)
	}
	j.Journaled(result("c1", agentv1.StepStatus_STEP_STATUS_FAILED))
	if hasEntry(t, store, "c1") {
		t.Error("the entry stays")
	}
	if err := j.save("p", "c2", []byte("e2")); err != nil {
		t.Fatal(err)
	}
	fsys.err = errors.New("disk says no")
	j.Journaled(result("c2", agentv1.StepStatus_STEP_STATUS_FAILED)) // logs to the discarded log: must not panic
	if !hasEntry(t, store, "c2") {
		t.Error("an entry that could not be removed went")
	}
}

func TestARemovedEntryIsLoggedAndOneThatCouldNotBeRemovedIsLoggedAsSuchOnly(t *testing.T) {
	var logs bytes.Buffer
	fsys := &removeFailsFS{FS: hostsetup.OS{}}
	j, store := journalOver(t, fsys, slog.New(slog.NewTextHandler(&logs, nil)))
	if err := j.save("p", "c1", []byte("e1")); err != nil {
		t.Fatal(err)
	}
	j.Journaled(result("c1", agentv1.StepStatus_STEP_STATUS_FAILED))
	for _, want := range []string{"level=INFO", "removed the entry of the restore journal", "command_id=c1"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log lacks %q: %s", want, logs.String())
		}
	}
	logs.Reset()
	if err := j.save("p", "c2", []byte("e2")); err != nil {
		t.Fatal(err)
	}
	fsys.err = errors.New("disk says no")
	j.Journaled(result("c2", agentv1.StepStatus_STEP_STATUS_FAILED))
	for _, want := range []string{"level=ERROR", "cannot remove the entry of the restore journal; remove it by hand", "command_id=c2",
		"path=" + store.Path("c2"), "disk says no"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log lacks %q: %s", want, logs.String())
		}
	}
	if strings.Contains(logs.String(), "removed the entry") {
		t.Errorf("a removal that failed is logged as done: %s", logs.String())
	}
}

func TestAKeptEntryGoesOnlyWithASuccessfulResult(t *testing.T) {
	for status, wantStays := range map[agentv1.StepStatus]bool{
		agentv1.StepStatus_STEP_STATUS_SUCCEEDED: false,
		agentv1.StepStatus_STEP_STATUS_FAILED:    true,
		agentv1.StepStatus_STEP_STATUS_CANCELLED: true,
	} {
		t.Run(status.String(), func(t *testing.T) {
			j, store := journalOver(t, hostsetup.OS{}, nil)
			if err := j.save("p", "c1", []byte("e1")); err != nil {
				t.Fatal(err)
			}
			j.keep("c1")
			j.Journaled(result("c1", status))
			if got := hasEntry(t, store, "c1"); got != wantStays {
				t.Errorf("entry present: %v, want %v", got, wantStays)
			}
		})
	}
}

func TestAnEntryThatIsNotKeptGoesWithAnyResult(t *testing.T) {
	for _, status := range []agentv1.StepStatus{agentv1.StepStatus_STEP_STATUS_SUCCEEDED, agentv1.StepStatus_STEP_STATUS_FAILED} {
		j, store := journalOver(t, hostsetup.OS{}, nil)
		if err := j.save("p", "c1", []byte("e1")); err != nil {
			t.Fatal(err)
		}
		j.Journaled(result("c1", status))
		if hasEntry(t, store, "c1") {
			t.Errorf("%v: the entry stays", status)
		}
	}
}

func TestTheDefaultsOfARecovery(t *testing.T) {
	got := RecoveryOptions{}.withDefaults()
	if got.Timeout != 5*time.Minute || got.Grace != 10*time.Second || got.Logger == nil {
		t.Errorf("defaults = %v, %v, %v", got.Timeout, got.Grace, got.Logger)
	}
	for _, zero := range []time.Duration{0, -time.Second} {
		got := RecoveryOptions{Timeout: zero, Grace: zero}.withDefaults()
		if got.Timeout != RecoveryTimeout || got.Grace != recoveryGrace {
			t.Errorf("%v: %v, %v", zero, got.Timeout, got.Grace)
		}
	}
}

func TestTheSmallestPositiveTimeAndGraceAreKept(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	got := RecoveryOptions{Timeout: time.Nanosecond, Grace: time.Nanosecond, Logger: logger}.withDefaults()
	if got.Timeout != time.Nanosecond || got.Grace != time.Nanosecond || got.Logger != logger {
		t.Errorf("options = %+v", got)
	}
}
