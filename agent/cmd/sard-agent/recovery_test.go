// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/consolerestore"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// completedRestore leaves the directory of a restore killed after its move:
// a marker and no root.
func completedRestore(t *testing.T, dir string, ids ...string) *consolerestore.Engine {
	t.Helper()
	for _, id := range ids {
		if err := os.MkdirAll(filepath.Join(dir, ".sard-staging", id), 0o700); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, ".sard-staging", id, "marker"), []byte(`{"destination":"s1","files_restored":2,"bytes_restored":4}`), 0o600)
	}
	return consolerestore.New(consolerestore.Options{
		Dir: dir, FS: hostsetup.OS{}, Owner: hostsetup.Attrs{UID: os.Getuid(), GID: os.Getgid(), Mode: 0o700}, Logger: slog.New(slog.DiscardHandler),
	})
}

func present(dir, id string) bool {
	_, err := os.Stat(filepath.Join(dir, ".sard-staging", id))
	return err == nil
}

// Scenario: Убитое после переноса восстановление сообщает об успехе; the directory goes once the result is journaled.
func TestTheDirectoryOfACompletedRestoreGoesOnlyOnceItsResultIsJournaled(t *testing.T) {
	dir := t.TempDir()
	r := newRecovery(completedRestore(t, dir, "c1"))
	got := r.interrupted("c1", agentv1.Action_ACTION_RESTORE)
	if got == nil || !got.Succeeded || got.Output.GetFilesRestored() != 2 {
		t.Fatalf("Interruption = %+v", got)
	}
	r.executorStarted()
	if !present(dir, "c1") {
		t.Fatal("the directory went before the result was journaled")
	}
	r.journaled(&agentv1.StepResult{CommandId: "c1", Status: agentv1.StepStatus_STEP_STATUS_SUCCEEDED, Output: &agentv1.StepResult_Restore{Restore: got.Output}})
	if present(dir, "c1") {
		t.Error("the directory stays after its result was journaled")
	}
}

// Scenario: Временный каталог неизвестной команды удаляется при старте.
func TestTheDirectoryOfACommandTheExecutorNeverAskedAboutGoesWhenTheExecutorHasStarted(t *testing.T) {
	dir := t.TempDir()
	r := newRecovery(completedRestore(t, dir, "c1", "c9"))
	r.interrupted("c1", agentv1.Action_ACTION_RESTORE)
	r.executorStarted()
	if present(dir, "c9") || !present(dir, "c1") {
		t.Errorf("c9 present: %v, c1 present: %v", present(dir, "c9"), present(dir, "c1"))
	}
}

func TestAFailedResultDoesNotRemoveWhatItNames(t *testing.T) {
	dir := t.TempDir()
	r := newRecovery(completedRestore(t, dir, "c1"))
	r.journaled(&agentv1.StepResult{CommandId: "c1", Status: agentv1.StepStatus_STEP_STATUS_FAILED, Output: &agentv1.StepResult_Restore{Restore: &agentv1.RestoreOutput{Leftover: "x"}}})
	r.journaled(&agentv1.StepResult{CommandId: "c1", Status: agentv1.StepStatus_STEP_STATUS_SUCCEEDED})
	if !present(dir, "c1") {
		t.Error("c1 was removed")
	}
}

// Scenario: Временный каталог неизвестной команды удаляется при старте (the wiring of the agent).
func TestTheStartedAgentSettlesTheDirectoryOfACommandItDoesNotKnow(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ca := writeIdentity(t, dir)
	restoreDir := filepath.Join(dir, "restore")
	completedRestore(t, restoreDir, "c9")
	cfg := "server:\n  address: 127.0.0.1:1\n" +
		"tls: {ca_file: " + ca + ", cert_file: " + dir + "/agent.pem, key_file: " + dir + "/agent.key}\n" +
		"executor: {state_dir: " + dir + "/state}\nrestore: {dir: " + restoreDir + "}\n" + withRestic(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var out, errOut bytes.Buffer
	if code := run(ctx, []string{"--config", writeConfig(t, cfg)}, &out, &errOut, fixedHostname); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errOut.String())
	}
	if present(restoreDir, "c9") {
		t.Error("the directory of a command nobody waits for is still there")
	}
}

// completedDatabase leaves the journal entry of a restore of a database killed
// after pg_restore: the result is in it.
func completedDatabase(t *testing.T, state string, ids ...string) *consolerestore.Engine {
	t.Helper()
	for _, id := range ids {
		if err := os.MkdirAll(filepath.Join(state, "pg-restore"), 0o700); err != nil {
			t.Fatal(err)
		}
		entry := `{"command_id":"` + id + `","connection":{"host":"db","port":5432,"user":"u","password_ref":"p"},"database":{"name":"d","oid":7},"result":{"database":"d","tables_restored":3,"roles_restored":1}}`
		writeFile(t, filepath.Join(state, "pg-restore", id+".json"), []byte(entry), 0o600)
	}
	return consolerestore.New(consolerestore.Options{
		Dir: filepath.Join(state, "restore"), FS: hostsetup.OS{}, Owner: hostsetup.Attrs{UID: os.Getuid(), GID: os.Getgid(), Mode: 0o700}, Logger: slog.New(slog.DiscardHandler),
		Postgres: consolerestore.PostgresOptions{StateDir: state},
	})
}

func journalHas(state, id string) bool {
	_, err := os.Stat(filepath.Join(state, "pg-restore", id+".json"))
	return err == nil
}

// Scenario: Убитое после завершения pg_restore восстановление сообщает об успехе из журнала отката.
func TestTheEntryOfACompletedDatabaseRestoreGoesOnlyOnceItsResultIsJournaled(t *testing.T) {
	state := t.TempDir()
	r := newRecovery(completedDatabase(t, state, "c1"))
	got := r.interrupted("c1", agentv1.Action_ACTION_RESTORE)
	if got == nil || !got.Succeeded || got.Output.GetDatabase() != "d" || got.Output.GetTablesRestored() != 3 || got.Output.GetRolesRestored() != 1 {
		t.Fatalf("Interruption = %+v", got)
	}
	r.executorStarted()
	if !journalHas(state, "c1") {
		t.Fatal("the entry went before the result was journaled")
	}
	r.journaled(&agentv1.StepResult{CommandId: "c1", Status: agentv1.StepStatus_STEP_STATUS_SUCCEEDED, Output: &agentv1.StepResult_Restore{Restore: got.Output}})
	if journalHas(state, "c1") {
		t.Error("the entry stays after its result was journaled")
	}
}

func TestTheEntryOfACommandTheExecutorNeverAskedAboutGoesWhenTheExecutorHasStarted(t *testing.T) {
	state := t.TempDir()
	r := newRecovery(completedDatabase(t, state, "c1", "c9"))
	r.interrupted("c1", agentv1.Action_ACTION_RESTORE)
	r.executorStarted()
	if journalHas(state, "c9") || !journalHas(state, "c1") {
		t.Errorf("c9: %v, c1: %v", journalHas(state, "c9"), journalHas(state, "c1"))
	}
}

func TestTheEntryOfAFailedRestoreOfADatabaseWithNothingToDropGoesWithItsResult(t *testing.T) {
	state := t.TempDir()
	e := completedDatabase(t, state, "c0")
	writeFile(t, filepath.Join(state, "pg-restore", "c1.json"), []byte(`{"command_id":"c1","connection":{},"database":{"name":"d"}}`), 0o600)
	r := newRecovery(e)
	r.journaled(&agentv1.StepResult{CommandId: "c1", Status: agentv1.StepStatus_STEP_STATUS_FAILED, Output: &agentv1.StepResult_Restore{Restore: &agentv1.RestoreOutput{Leftover: "database d"}}})
	if journalHas(state, "c1") {
		t.Error("an entry of intents stays")
	}
}
