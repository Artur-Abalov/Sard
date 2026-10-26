// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/consolerestore"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/internal/pluginhost/journaltest"
	"github.com/Artur-Abalov/sard/agent/internal/restorejournal"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// journalOf is the journal of the state directory, with the entries given
// (command id → plugin) saved in it.
func journalOf(t *testing.T, state string, entries map[string]string) *pluginhost.Journal {
	t.Helper()
	store := restorejournal.New(restorejournal.Options{
		Dir: filepath.Join(state, "restore-journal"), FS: hostsetup.OS{}, Owner: hostsetup.Attrs{UID: os.Getuid(), GID: os.Getgid()},
	})
	for id, plugin := range entries {
		if err := store.Save(plugin, id, []byte("entry of "+id)); err != nil {
			t.Fatal(err)
		}
	}
	return pluginhost.NewJournal(store, slog.New(slog.DiscardHandler))
}

func registryOf(t *testing.T, plugins ...sdk.Plugin) *sdk.Registry {
	t.Helper()
	reg, err := sdk.NewRegistry(plugins...)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func recoveryWithJournal(t *testing.T, engine *consolerestore.Engine, journal *pluginhost.Journal, reg *sdk.Registry) *recovery {
	t.Helper()
	jr := pluginhost.RecoverJournal(pluginhost.RecoveryOptions{Journal: journal, Plugins: reg, Secrets: pluginhost.NewSecrets(nil, os.ReadFile, nil), Logger: slog.New(slog.DiscardHandler)})
	return newRecoveryWith(engine, journal, jr)
}

// Scenario: Остатки временных каталогов и записи журнала разбираются одним стартом.
func TestTheTemporaryDirectoriesAndTheEntriesOfTheJournalAreSettledByOneStart(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	if err := os.MkdirAll(filepath.Join(dir, ".sard-staging", "c1", "root"), 0o700); err != nil {
		t.Fatal(err)
	}
	var recovered []string
	p := &journaltest.Plugin{RecoverFn: func(_ context.Context, _ sdk.RecoveryHost, id string, _ []byte) sdk.Recovery {
		recovered = append(recovered, id)
		return sdk.NothingRestored()
	}}
	engine := consolerestore.New(consolerestore.Options{
		Dir: dir, FS: hostsetup.OS{}, Owner: hostsetup.Attrs{UID: os.Getuid(), GID: os.Getgid(), Mode: 0o700}, Logger: slog.New(slog.DiscardHandler),
	})
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	r := recoveryWithJournal(t, engine, journalOf(t, state, map[string]string{"c2": "journaltest"}), registryOf(t, p))
	if present(dir, "c1") || len(recovered) != 1 || recovered[0] != "c2" {
		t.Errorf("c1 present: %v, recovered %q", present(dir, "c1"), recovered)
	}
	assertNothingRestored(t, r, "c1")
	assertNothingRestored(t, r, "c2")
}

func assertNothingRestored(t *testing.T, r *recovery, id string) {
	t.Helper()
	const nothing = "interrupted: agent restarted before the step finished; nothing was restored"
	if got := r.interrupted(id, agentv1.Action_ACTION_RESTORE); got == nil || got.Message != nothing {
		t.Errorf("%s: Interruption = %+v", id, got)
	}
}

// Scenario: Запись удаляет хост после записи результата (the wiring of the agent).
func TestTheEntryOfTheJournalGoesWhenTheResultIsJournaled(t *testing.T) {
	state := t.TempDir()
	journal := journalOf(t, state, map[string]string{"c2": "journaltest", "c3": "journaltest"})
	engine := consolerestore.New(consolerestore.Options{Dir: t.TempDir(), FS: hostsetup.OS{}, Logger: slog.New(slog.DiscardHandler)})
	r := recoveryWithJournal(t, engine, journal, registryOf(t, &journaltest.Plugin{}))
	r.interrupted("c2", agentv1.Action_ACTION_RESTORE)
	entry := func(id string) bool {
		_, err := os.Stat(filepath.Join(state, "restore-journal", id+".json"))
		return err == nil
	}
	if !entry("c2") {
		t.Fatal("the entry went before the result was journaled")
	}
	r.journaled(&agentv1.StepResult{CommandId: "c2", Status: agentv1.StepStatus_STEP_STATUS_FAILED})
	if entry("c2") {
		t.Error("the entry stays after its result was journaled")
	}
	r.executorStarted()
	if entry("c3") {
		t.Error("the entry of a command nobody waits for stays")
	}
}

// Scenario: Разбор записей идёт без связи с сервером. The recovery of the
// journal is over before the agent dials the server for the first time.
func TestTheEntriesAreRecoveredBeforeTheAgentDialsTheServer(t *testing.T) {
	dir := t.TempDir()
	ca := writeIdentity(t, dir)
	state := filepath.Join(dir, "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	journalOf(t, state, map[string]string{"c1": "journaltest"})
	addr, accepted, first := signalingServer(t)
	var dialedBefore atomic.Bool
	var recoveredAt atomic.Int32
	p := &journaltest.Plugin{RecoverFn: func(context.Context, sdk.RecoveryHost, string, []byte) sdk.Recovery {
		recoveredAt.Store(accepted.Load() + 1)
		select {
		case <-first:
			dialedBefore.Store(true)
		default:
		}
		return sdk.NothingRestored()
	}}
	cfg := writeConfig(t, "server:\n  address: "+addr+"\n"+
		"tls: {ca_file: "+ca+", cert_file: "+dir+"/agent.pem, key_file: "+dir+"/agent.key}\n"+
		"executor: {state_dir: "+state+"}\n"+withRestic(t))
	ctx, cancel := contextUntil(eventWait, first)
	defer cancel()
	deps := selfDepsFor(realEnrollClock{})
	deps.runtime = agentRuntime{journalPlugins: registryOf(t, p)}
	var out, errOut bytes.Buffer
	if code := runAgentCmd(ctx, []string{"--config", cfg}, &out, &errOut, fixedHostname, deps); code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errOut.String())
	}
	if recoveredAt.Load() == 0 {
		t.Fatal("the entry was not recovered")
	}
	if dialedBefore.Load() {
		t.Error("the agent had dialed the server when the entry was recovered")
	}
}

func TestTheJournalOfTheAgentLivesInTheStateDirectoryOfTheExecutor(t *testing.T) {
	cfg := mustConfig(t, "server:\n  address: s:1\nexecutor:\n  state_dir: /srv/state\n")
	if got := restoreJournalDir(cfg); got != "/srv/state/restore-journal" {
		t.Errorf("journal directory = %q", got)
	}
	cfg = mustConfig(t, "server:\n  address: s:1\n")
	if got := restoreJournalDir(cfg); got != filepath.Join(defaultExecutorStateDir, "restore-journal") {
		t.Errorf("default journal directory = %q", got)
	}
}

func mustConfig(t *testing.T, yaml string) config.Config {
	t.Helper()
	cfg, err := config.Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// The outcome a plugin made of its entry is the result of the interrupted command,
// not the "nothing was restored" the staging directory would give.
func TestTheOutcomeOfAPluginIsTheResultOfTheInterruptedCommand(t *testing.T) {
	state := t.TempDir()
	p := &journaltest.Plugin{RecoverFn: func(context.Context, sdk.RecoveryHost, string, []byte) sdk.Recovery {
		return sdk.Restored(sdk.RestoreResult{Database: "x1", Tables: 3})
	}}
	engine := consolerestore.New(consolerestore.Options{Dir: t.TempDir(), FS: hostsetup.OS{}, Logger: slog.New(slog.DiscardHandler)})
	r := recoveryWithJournal(t, engine, journalOf(t, state, map[string]string{"c2": "journaltest"}), registryOf(t, p))
	got := r.interrupted("c2", agentv1.Action_ACTION_RESTORE)
	if got == nil || !got.Succeeded || got.Output.GetDatabase() != "x1" || got.Output.GetTablesRestored() != 3 {
		t.Fatalf("Interruption = %+v", got)
	}
	if other := r.interrupted("c9", agentv1.Action_ACTION_RESTORE); other == nil || other.Succeeded {
		t.Errorf("a command without an entry: %+v", other)
	}
}
