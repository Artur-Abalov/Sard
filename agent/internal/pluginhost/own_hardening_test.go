// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/consolerestore"
	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/internal/pluginhost/journaltest"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// dumpsWith is the plugin T that streams one file of the snapshot with the context given
// and keeps what Dump returned.
func dumpsWith(ctxOf func(step context.Context) (context.Context, func()), got *error) *journaltest.Plugin {
	return &journaltest.Plugin{RestoreFn: func(ctx context.Context, h sdk.RestoreHost, _ sdk.Config, req sdk.RestoreRequest) (sdk.RestoreResult, error) {
		own, done := ctxOf(ctx)
		defer done()
		*got = h.Dump(own, req.SnapshotID, "/a", io.Discard)
		return sdk.RestoreResult{}, nil
	}}
}

// Scenario: Остановка потока собственным контекстом плагина не прерывает шаг.
func TestAStreamStoppedByThePluginIsReportedAsStoppedWithTheCauseThePluginGave(t *testing.T) {
	stopper := errors.New("the plugin has enough")
	var got error
	r := newOwnRig(t, dumpsWith(func(step context.Context) (context.Context, func()) {
		own, cancel := context.WithCancelCause(step)
		cancel(stopper)
		return own, func() {}
	}, &got))
	r.repo.dump = func(ctx context.Context, _, _ string, _ io.Writer) error {
		<-ctx.Done()
		return ctx.Err()
	}
	if _, err := r.run(r.step()); err != nil {
		t.Fatal(err)
	}
	var re *sdk.RestoreError
	if !errors.Is(got, stopper) || errors.As(got, &re) || !strings.Contains(got.Error(), "the snapshot stream was stopped") {
		t.Errorf("Dump = %v", got)
	}
}

// Scenario: Отмена шага во время потока снимка даёт INTERRUPTED, not a stopped stream of the plugin.
func TestAStreamThatTheStepEndsIsAnInterruptedRepositoryFailureNotAStoppedStream(t *testing.T) {
	var got error
	r := newOwnRig(t, dumpsWith(func(step context.Context) (context.Context, func()) { return step, func() {} }, &got))
	stepCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.repo.dump = func(ctx context.Context, _, _ string, _ io.Writer) error {
		cancel()
		<-ctx.Done()
		return ctx.Err()
	}
	h, _ := r.handlers.Handler("journaltest")
	if _, err := h.Run(stepCtx, r.step(), &reporter{}); err != nil {
		t.Fatal(err)
	}
	var re *sdk.RestoreError
	if !errors.As(got, &re) || re.Reason != "INTERRUPTED" || !errors.Is(got, context.Canceled) {
		t.Errorf("Dump = %#v", got)
	}
}

// Scenario: Хост без журнала не запускает собственное восстановление плагина.
func TestAHostWithoutAJournalRejectsTheRestoreOfAPluginThatRestoresItself(t *testing.T) {
	r := newOwnRig(t, &journaltest.Plugin{})
	engine := consolerestore.New(consolerestore.Options{Dir: r.dir, FS: hostsetup.OS{}, Owner: r.attrs(), Logger: slog.New(slog.DiscardHandler)})
	repos := func(string, string, io.Writer) (restic.Repository, bool) { return r.repo, true }
	handlers, err := pluginhost.NewHandlers(r.reg, r.secrets, repos, filepath.Join(t.TempDir(), "verify"), pluginhost.WithConsoleRestore(engine))
	if err != nil {
		t.Fatal(err)
	}
	h, _ := handlers.Handler("journaltest")
	res, err := h.Run(context.Background(), r.step(), &reporter{})
	if res != nil || !errors.Is(err, executor.ErrRejected) || !strings.Contains(err.Error(), "the restore journal is not enabled on this host") {
		t.Errorf("res = %v, err = %v", res, err)
	}
}

// A refusal that leaves nothing behind says nothing about leftovers.
func TestARefusalThatLeavesNothingBehindIsNotLoggedAsALeftover(t *testing.T) {
	r := newOwnRig(t, refuses(&sdk.RestoreError{Reason: "TEST_REFUSED", Stage: "check", Detail: "no luck"}))
	if _, err := r.run(r.step()); err == nil {
		t.Fatal("no error")
	}
	if log := r.logs.String(); strings.Contains(log, "could not remove") || strings.Contains(log, "level=ERROR") {
		t.Errorf("log = %s", log)
	}
}

func TestARefusalThatLeavesSomethingBehindIsLoggedWithTheCommandThePluginAndWhatIsLeft(t *testing.T) {
	r := newOwnRig(t, refuses(&sdk.RestoreError{Reason: "TEST_REFUSED", Stage: "restore", Leftover: []string{"thing a", "thing b"}, Cleanup: "boom"}))
	if _, err := r.run(r.step()); err == nil {
		t.Fatal("no error")
	}
	log := r.logs.String()
	for _, want := range []string{"level=ERROR", "command_id=c1", "plugin=journaltest", `left="thing a, thing b"`, "error=boom"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %s: %s", want, log)
		}
	}
}

// A secret of one byte is still a secret.
func TestAnEntryHoldingASecretOfOneByteIsNotSaved(t *testing.T) {
	var saveErr error
	p := &journaltest.Plugin{RestoreFn: func(_ context.Context, h sdk.RestoreHost, _ sdk.Config, _ sdk.RestoreRequest) (sdk.RestoreResult, error) {
		if _, err := secretOf(h, "pg-restore"); err != nil {
			return sdk.RestoreResult{}, err
		}
		saveErr = h.Journal().Save([]byte("a-q-b"))
		return sdk.RestoreResult{}, nil
	}}
	r := newOwnRig(t, p)
	h, _ := r.handlers.Handler("journaltest")
	if _, err := h.Run(context.Background(), r.step(), &reporter{snapshot: map[string][]byte{"pg-restore": []byte("q")}}); err != nil {
		t.Fatal(err)
	}
	if saveErr == nil || r.hasEntry("c1") {
		t.Errorf("Save = %v, entry saved: %v", saveErr, r.hasEntry("c1"))
	}
}

// Scenario: Запись, которую разбор просил сохранить, уходит после удачного повтора команды.
func TestAnEntryTheRecoveryAskedToKeepGoesWhenTheRetryOfTheCommandSucceeds(t *testing.T) {
	for status, wantStays := range map[agentv1.StepStatus]bool{
		agentv1.StepStatus_STEP_STATUS_SUCCEEDED: false,
		agentv1.StepStatus_STEP_STATUS_FAILED:    true,
	} {
		t.Run(status.String(), func(t *testing.T) {
			var ev events
			r := newOwnRig(t, recovering("journaltest", &ev, outcomeOf(sdk.LeftBehind([]string{"thing a"}, "boom", true))))
			r.saveEntry("c1")
			r.recoverAll(0)
			r.journal.Journaled(&agentv1.StepResult{CommandId: "c1", Status: status})
			if got := r.hasEntry("c1"); got != wantStays {
				t.Errorf("entry present: %v, want %v", got, wantStays)
			}
		})
	}
}

// ---- the recovery at start, as the operator sees it in the log ----

func (r *ownRig) saveEntry(id string) {
	r.t.Helper()
	if err := r.store.Save("journaltest", id, []byte("e1")); err != nil {
		r.t.Fatal(err)
	}
}

func assertLogged(t *testing.T, log string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(log, want) {
			t.Errorf("the log lacks %q: %s", want, log)
		}
	}
}

func TestAJournalThatCannotBeReadIsLoggedAndNothingIsRecovered(t *testing.T) {
	var ev events
	r := newOwnRig(t, recovering("journaltest", &ev, nil))
	if err := os.MkdirAll(r.state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.journalDir(), []byte("a file, not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := r.recoverAll(0)
	assertLogged(t, r.logs.String(), "level=ERROR", "cannot read the restore journal; judge the restores by hand", "error=")
	if rec.Interrupted("c1", restore) != nil || len(ev.all()) != 0 {
		t.Errorf("recovered %q", ev.all())
	}
}

func TestARecoveryThatFailedIsLoggedWithThePluginTheCommandAndThePath(t *testing.T) {
	var ev events
	r := newOwnRig(t, recovering("journaltest", &ev, func(context.Context, sdk.RecoveryHost, string, []byte) sdk.Recovery { panic("it broke") }))
	r.saveEntry("c1")
	r.recoverAll(0)
	assertLogged(t, r.logs.String(), "level=ERROR", "the recovery of an entry of the restore journal failed; judge the restore by hand",
		"plugin=journaltest", "command_id=c1", "path="+r.entryPath("c1"), `error="the plugin panicked: it broke"`)
}

func TestTheOutcomeOfEachRecoveryIsLogged(t *testing.T) {
	tests := map[string]struct {
		outcome sdk.Recovery
		want    []string
	}{
		"nothing restored": {sdk.NothingRestored(), []string{"level=INFO", "recovered an interrupted restore: nothing was restored", "command_id=c1"}},
		"completed":        {sdk.Restored(sdk.RestoreResult{Database: "x1"}), []string{"level=INFO", "the restore completed before the agent was restarted", "command_id=c1"}},
		"left behind": {sdk.LeftBehind([]string{"thing a", "thing b"}, "boom", false), []string{
			"level=ERROR", "the recovery of an interrupted restore could not undo everything; remove it by hand", "command_id=c1", `left="thing a, thing b"`, "error=boom"}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var ev events
			r := newOwnRig(t, recovering("journaltest", &ev, outcomeOf(tc.outcome)))
			r.saveEntry("c1")
			r.recoverAll(0)
			assertLogged(t, r.logs.String(), tc.want...)
		})
	}
}

func TestTheLogOfARecoveryKeepsTheLevelsOfThePluginAndTakesAnUnknownOneAsInfo(t *testing.T) {
	var ev events
	r := newOwnRig(t, recovering("journaltest", &ev, func(_ context.Context, h sdk.RecoveryHost, _ string, _ []byte) sdk.Recovery {
		h.Log(sdk.LevelError, "msg-error")
		h.Log(sdk.LevelWarn, "msg-warn")
		h.Log(sdk.LevelInfo, "msg-info")
		h.Log(sdk.Level(99), "msg-unknown")
		return sdk.NothingRestored()
	}))
	r.saveEntry("c1")
	r.recoverAll(0)
	log := r.logs.String()
	assertLogged(t, log, "level=ERROR msg=msg-error", "level=WARN msg=msg-warn", "level=INFO msg=msg-info", "level=INFO msg=msg-unknown")
}

// Only the restore of a command is told about its recovery; a backup of the same id is not.
func TestTheOutcomeOfARecoveryIsOnlyForTheRestoreOfTheCommand(t *testing.T) {
	var ev events
	r := newOwnRig(t, recovering("journaltest", &ev, outcomeOf(sdk.Restored(sdk.RestoreResult{Database: "x1"}))))
	r.saveEntry("c1")
	rec := r.recoverAll(0)
	if got := rec.Interrupted("c1", agentv1.Action_ACTION_BACKUP); got != nil {
		t.Errorf("a backup was told %v", got)
	}
	if got := rec.Interrupted("c2", restore); got != nil {
		t.Errorf("a command without an entry was told %v", got)
	}
	// The executor did not ask about the restore of c1 yet, so the entry is not waited for.
	rec.ExecutorStarted()
	if r.hasEntry("c1") {
		t.Error("the entry stays though nothing waits for it")
	}
}

func TestACommandTheExecutorAskedAboutKeepsItsEntryUntilTheResultIsRecorded(t *testing.T) {
	var ev events
	r := newOwnRig(t, recovering("journaltest", &ev, outcomeOf(sdk.Restored(sdk.RestoreResult{Database: "x1"}))))
	r.saveEntry("c1")
	rec := r.recoverAll(0)
	if got := rec.Interrupted("c1", restore); got == nil || !got.Succeeded {
		t.Fatalf("Interrupted = %v", got)
	}
	answered := make(chan struct{})
	go func() {
		rec.ExecutorStarted()
		rec.Interrupted("c1", restore) // must not wait for a lock ExecutorStarted kept
		close(answered)
	}()
	select {
	case <-answered:
	case <-time.After(5 * time.Second):
		t.Fatal("the recovery does not answer after the executor started")
	}
	if !r.hasEntry("c1") {
		t.Error("the entry went before the result was recorded")
	}
}
