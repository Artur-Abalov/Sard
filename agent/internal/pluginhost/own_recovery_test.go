// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/internal/pluginhost/journaltest"
	"github.com/Artur-Abalov/sard/agent/internal/role"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
	"google.golang.org/protobuf/proto"
)

// blockedRegistry plays the handlers of an agent that is killed in the middle
// of its steps: every step blocks until it is cancelled.
type blockedRegistry struct {
	started chan string
}

func (b *blockedRegistry) Handler(string) (executor.Handler, bool) { return b, true }

func (*blockedRegistry) Actions() []agentv1.Action { return []agentv1.Action{restore} }

func (b *blockedRegistry) Run(ctx context.Context, step *agentv1.RunStep, _ executor.Reporter) (*agentv1.StepResult, error) {
	b.started <- step.GetCommandId()
	<-ctx.Done()
	return nil, ctx.Err()
}

// killSteps starts the steps in an executor over the state directory of the rig
// and aborts it, as SIGKILL would leave things: the steps are journaled, they
// have no result.
func (r *ownRig) killSteps(ids ...string) {
	r.t.Helper()
	b := &blockedRegistry{started: make(chan string, len(ids))}
	exec, err := executor.New(executor.Options{Role: role.Source, Handlers: b, Sink: newSink(), StateDir: r.state, Repositories: []string{"main"}})
	if err != nil {
		r.t.Fatal(err)
	}
	for _, id := range ids {
		step := r.step()
		step.CommandId = id
		exec.Submit(step)
		<-b.started
	}
	if err := exec.Shutdown(context.Background(), executor.ShutdownAbort); err != nil {
		r.t.Fatal(err)
	}
}

// start is the start of the agent: the journal is read and recovered, and the
// executor is made over the state directory. The plugins are those of the rig.
func (r *ownRig) start(timeout time.Duration) (*executor.Executor, *sink, *pluginhost.JournalRecovery) {
	r.t.Helper()
	rec := r.recoverAll(timeout)
	s := newSink()
	exec, err := executor.New(executor.Options{
		Role: role.Source, Handlers: r.handlers, Sink: s, StateDir: r.state, Repositories: []string{"main"},
		Interrupted: rec.Interrupted, Journaled: r.journal.Journaled,
	})
	if err != nil {
		r.t.Fatal(err)
	}
	rec.ExecutorStarted()
	r.t.Cleanup(func() { _ = exec.Close(context.Background()) })
	return exec, s, rec
}

func (r *ownRig) recoverAll(timeout time.Duration) *pluginhost.JournalRecovery {
	r.t.Helper()
	return pluginhost.RecoverJournal(pluginhost.RecoveryOptions{
		Journal: r.journal, Plugins: r.reg, Secrets: r.secrets, Logger: slog.New(slog.NewTextHandler(r.logs, nil)),
		Timeout: timeout, Grace: 50 * time.Millisecond,
	})
}

// pending is the result of the command that the executor keeps for the server.
func pending(t *testing.T, exec *executor.Executor, id string) *agentv1.StepResult {
	t.Helper()
	for _, res := range exec.PendingResults() {
		if res.GetCommandId() == id {
			return res
		}
	}
	t.Fatalf("no result of %s", id)
	return nil
}

// events is what the plugins did, in order.
type events struct {
	mu  sync.Mutex
	log []string
}

func (e *events) add(format string, args ...any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.log = append(e.log, fmt.Sprintf(format, args...))
}

func (e *events) all() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.log)
}

// recovering is the plugin T whose recovery gives the outcome.
func recovering(name string, ev *events, outcome func(ctx context.Context, h sdk.RecoveryHost, id string, entry []byte) sdk.Recovery) *journaltest.Plugin {
	return &journaltest.Plugin{PluginName: name, RecoverFn: func(ctx context.Context, h sdk.RecoveryHost, id string, entry []byte) sdk.Recovery {
		ev.add("recover %s %s", id, entry)
		if outcome == nil {
			return sdk.NothingRestored()
		}
		return outcome(ctx, h, id, entry)
	}}
}

func outcomeOf(rec sdk.Recovery) func(context.Context, sdk.RecoveryHost, string, []byte) sdk.Recovery {
	return func(context.Context, sdk.RecoveryHost, string, []byte) sdk.Recovery { return rec }
}

// Scenario: При старте плагин получает только свои записи.
func TestAtStartAPluginGetsOnlyItsOwnEntries(t *testing.T) {
	var ev1, ev2 events
	r := newOwnRig(t, recovering("journaltest", &ev1, nil), recovering("journaltest2", &ev2, nil))
	for plugin, id := range map[string]string{"journaltest": "c1", "journaltest2": "c2"} {
		if err := r.store.Save(plugin, id, []byte("e-"+id)); err != nil {
			t.Fatal(err)
		}
	}
	r.recoverAll(0)
	if got := ev1.all(); !slices.Equal(got, []string{"recover c1 e-c1"}) {
		t.Errorf("T recovered %q", got)
	}
	if got := ev2.all(); !slices.Equal(got, []string{"recover c2 e-c2"}) {
		t.Errorf("U recovered %q", got)
	}
}

// Scenario: Запись плагина, которого нет в агенте, не трогается.
func TestAnEntryOfAPluginThatIsNotInTheAgentIsLeftAloneAndNamed(t *testing.T) {
	r := newOwnRig(t, &journaltest.Plugin{})
	r.killSteps("c1")
	if err := r.store.Save("gone", "c1", []byte("theirs")); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(r.entryPath("c1"))
	exec, s, _ := r.start(0)
	after, err := os.ReadFile(r.entryPath("c1"))
	if err != nil || string(before) != string(after) {
		t.Errorf("the entry changed: %q, %v", after, err)
	}
	log := r.logs.String()
	if !strings.Contains(log, "level=ERROR") || !strings.Contains(log, "gone") || !strings.Contains(log, "c1") {
		t.Errorf("log = %s", log)
	}
	assertCleanupFailedNaming(t, pending(t, exec, "c1"), r.entryPath("c1"))
	assertAcceptsSteps(t, r, exec, s)
}

// assertCleanupFailedNaming: the result is FAILED with CLEANUP_FAILED and leftover is the path.
func assertCleanupFailedNaming(t *testing.T, res *agentv1.StepResult, path string) {
	t.Helper()
	if res.GetStatus() != agentv1.StepStatus_STEP_STATUS_FAILED || !strings.HasPrefix(res.GetMessage(), "CLEANUP_FAILED (rollback): "+executor.InterruptedMessage+"; left behind: "+path+" (") ||
		!strings.HasSuffix(res.GetMessage(), "); remove it by hand") || res.GetRestore().GetLeftover() != path {
		t.Errorf("result = %v, want leftover %s", res, path)
	}
}

// assertAcceptsSteps: the agent takes a step after the start.
func assertAcceptsSteps(t *testing.T, r *ownRig, exec *executor.Executor, s *sink) {
	t.Helper()
	step := r.step()
	step.CommandId = "c-next"
	exec.Submit(step)
	if res := s.result(t); res.GetCommandId() != "c-next" || res.GetStatus() != agentv1.StepStatus_STEP_STATUS_SUCCEEDED {
		t.Errorf("the next step ended with %v", res)
	}
}

// Scenario: Испорченная запись журнала называется и остаётся.
func TestACorruptEntryOfTheJournalIsNamedAndStays(t *testing.T) {
	for name, content := range map[string]string{
		"not JSON":           `{"plugin": `,
		"no plugin":          `{"command_id": "c1", "entry": ""}`,
		"another command id": `{"plugin": "journaltest", "command_id": "c7", "entry": ""}`,
	} {
		t.Run(name, func(t *testing.T) {
			r := newOwnRig(t, &journaltest.Plugin{})
			r.killSteps("c1")
			if err := os.MkdirAll(r.journalDir(), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(r.entryPath("c1"), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			exec, s, _ := r.start(0)
			if data, err := os.ReadFile(r.entryPath("c1")); err != nil || string(data) != content {
				t.Errorf("the file changed: %q, %v", data, err)
			}
			if log := r.logs.String(); !strings.Contains(log, "level=ERROR") || !strings.Contains(log, r.entryPath("c1")) {
				t.Errorf("log = %s", log)
			}
			assertCleanupFailedNaming(t, pending(t, exec, "c1"), r.entryPath("c1"))
			assertAcceptsSteps(t, r, exec, s)
		})
	}
}

// Scenario: Плагин разбирает свои записи до приёма первого шага.
func TestThePluginRecoversItsEntriesBeforeTheExecutorAcceptsTheFirstStep(t *testing.T) {
	var ev events
	p := &journaltest.Plugin{
		RestoreFn: func(context.Context, sdk.RestoreHost, sdk.Config, sdk.RestoreRequest) (sdk.RestoreResult, error) {
			ev.add("run")
			return sdk.RestoreResult{}, nil
		},
		RecoverFn: func(_ context.Context, _ sdk.RecoveryHost, id string, _ []byte) sdk.Recovery {
			ev.add("recover %s", id)
			return sdk.NothingRestored()
		},
	}
	r := newOwnRig(t, p)
	if err := r.store.Save("journaltest", "c1", []byte("e1")); err != nil {
		t.Fatal(err)
	}
	exec, s, _ := r.start(0)
	step := r.step()
	step.CommandId = "c2"
	exec.Submit(step)
	s.result(t)
	if got := ev.all(); !slices.Equal(got, []string{"recover c1", "run"}) {
		t.Errorf("order = %q", got)
	}
}

// Scenario: Разбор записей идёт без связи с сервером (the recovery needs no executor and no transport).
func TestTheEntriesAreRecoveredWithoutAnExecutorAndSoWithoutTheServer(t *testing.T) {
	var ev events
	r := newOwnRig(t, recovering("journaltest", &ev, nil))
	if err := r.store.Save("journaltest", "c1", []byte("e1")); err != nil {
		t.Fatal(err)
	}
	r.recoverAll(0)
	if got := ev.all(); !slices.Equal(got, []string{"recover c1 e1"}) {
		t.Errorf("recovered %q", got)
	}
}

// Scenario: Исход разбора становится результатом прерванной команды.
func TestTheOutcomeOfTheRecoveryIsTheResultOfTheInterruptedCommand(t *testing.T) {
	tests := []struct {
		name    string
		outcome sdk.Recovery
		status  agentv1.StepStatus
		message string
		output  *agentv1.RestoreOutput
	}{
		{"completed", sdk.Restored(sdk.RestoreResult{Database: "x1", Tables: 2}), agentv1.StepStatus_STEP_STATUS_SUCCEEDED,
			"agent restarted after the restore completed", &agentv1.RestoreOutput{Target: "x1", Database: "x1", TablesRestored: 2}},
		{"nothing restored", sdk.NothingRestored(), agentv1.StepStatus_STEP_STATUS_FAILED,
			"interrupted: agent restarted before the step finished; nothing was restored", nil},
		{"left behind", sdk.LeftBehind([]string{"thing a"}, "boom", false), agentv1.StepStatus_STEP_STATUS_FAILED,
			"CLEANUP_FAILED (rollback): interrupted: agent restarted before the step finished; left behind: thing a (boom); remove it by hand",
			&agentv1.RestoreOutput{Leftover: "thing a"}},
		{"several left behind", sdk.LeftBehind([]string{"thing a", "thing b"}, "boom", false), agentv1.StepStatus_STEP_STATUS_FAILED,
			"CLEANUP_FAILED (rollback): interrupted: agent restarted before the step finished; left behind: thing a, thing b (boom); remove it by hand",
			&agentv1.RestoreOutput{Leftover: "thing a, thing b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ev events
			r := newOwnRig(t, recovering("journaltest", &ev, outcomeOf(tt.outcome)))
			r.killSteps("c1")
			if err := r.store.Save("journaltest", "c1", []byte("e1")); err != nil {
				t.Fatal(err)
			}
			exec, _, _ := r.start(0)
			res := pending(t, exec, "c1")
			if res.GetStatus() != tt.status || res.GetMessage() != tt.message || !proto.Equal(res.GetRestore(), tt.output) {
				t.Errorf("result = %v", res)
			}
		})
	}
}

// A recovery with nothing left behind named is still told by the entry.
func TestARecoveryThatLeftSomethingBehindWithoutNamingItNamesTheEntry(t *testing.T) {
	var ev events
	r := newOwnRig(t, recovering("journaltest", &ev, outcomeOf(sdk.LeftBehind(nil, "", true))))
	r.killSteps("c1")
	if err := r.store.Save("journaltest", "c1", []byte("e1")); err != nil {
		t.Fatal(err)
	}
	exec, _, _ := r.start(0)
	if res := pending(t, exec, "c1"); res.GetRestore().GetLeftover() != r.entryPath("c1") {
		t.Errorf("result = %v", res)
	}
}

// Scenario: Разбор может переписать запись.
func TestTheRecoveryCanRewriteTheEntry(t *testing.T) {
	var ev events
	rewrite := func(_ context.Context, h sdk.RecoveryHost, _ string, _ []byte) sdk.Recovery {
		if err := h.Journal().Save([]byte("e2")); err != nil {
			t.Error(err)
		}
		return sdk.LeftBehind([]string{"thing a"}, "boom", true)
	}
	r := newOwnRig(t, recovering("journaltest", &ev, rewrite))
	if err := r.store.Save("journaltest", "c1", []byte("e1")); err != nil {
		t.Fatal(err)
	}
	r.recoverAll(0)
	if got := r.entryOf("c1"); got != "e2" {
		t.Errorf("entry = %q", got)
	}
}

// Scenario: Отказ чтения секрета при разборе даёт исход CLEANUP_FAILED.
func TestASecretThatCannotBeReadDuringTheRecoveryIsACleanupFailureThatNamesNoPath(t *testing.T) {
	var ev events
	failed := func(_ context.Context, h sdk.RecoveryHost, _ string, _ []byte) sdk.Recovery {
		_, err := secretOf(h, "gone")
		return sdk.LeftBehind([]string{"entry c1"}, err.Error(), true)
	}
	r := newOwnRig(t, recovering("journaltest", &ev, failed))
	r.killSteps("c1")
	if err := r.store.Save("journaltest", "c1", []byte("e1")); err != nil {
		t.Fatal(err)
	}
	exec, s, _ := r.start(0)
	res := pending(t, exec, "c1")
	if !strings.Contains(res.GetMessage(), "CLEANUP_FAILED") || strings.Contains(res.GetMessage(), "/etc/sard/gone") || !strings.Contains(res.GetMessage(), `"gone"`) {
		t.Errorf("message = %q", res.GetMessage())
	}
	assertAcceptsSteps(t, r, exec, s)
}

func TestTheRecoveryReadsASecretOfTheHostAndLogs(t *testing.T) {
	var ev events
	var value []byte
	read := func(_ context.Context, h sdk.RecoveryHost, _ string, _ []byte) sdk.Recovery {
		value, _ = secretOf(h, "pg-restore")
		h.Log(sdk.LevelWarn, "looking at c1")
		return sdk.NothingRestored()
	}
	r := newOwnRig(t, recovering("journaltest", &ev, read))
	if err := r.store.Save("journaltest", "c1", []byte("e1")); err != nil {
		t.Fatal(err)
	}
	r.recoverAll(0)
	log := r.logs.String()
	if string(value) != secretP2 || !strings.Contains(log, "looking at c1") || !strings.Contains(log, "level=WARN") || !strings.Contains(log, "journaltest") {
		t.Errorf("secret %q, log %s", value, log)
	}
	if strings.Contains(log, secretP2) {
		t.Errorf("the secret is in the log: %s", log)
	}
}

func TestTheRecoveryCannotSaveTheValueOfASecretItRead(t *testing.T) {
	var ev events
	var saveErr error
	leak := func(_ context.Context, h sdk.RecoveryHost, _ string, _ []byte) sdk.Recovery {
		value, _ := secretOf(h, "pg-restore")
		saveErr = h.Journal().Save(value)
		return sdk.NothingRestored()
	}
	r := newOwnRig(t, recovering("journaltest", &ev, leak))
	if err := r.store.Save("journaltest", "c1", []byte("e1")); err != nil {
		t.Fatal(err)
	}
	r.recoverAll(0)
	if saveErr == nil || strings.Contains(saveErr.Error(), secretP2) || r.entryOf("c1") != "e1" {
		t.Errorf("error = %v, entry %q", saveErr, r.entryOf("c1"))
	}
}

// Scenario: Зависший разбор не держит агента дольше срока.
func TestARecoveryThatHangsIsCancelledAfterItsTimeAndTheEntryStays(t *testing.T) {
	var deadline time.Time
	var ev events
	hang := func(ctx context.Context, _ sdk.RecoveryHost, _ string, _ []byte) sdk.Recovery {
		deadline, _ = ctx.Deadline()
		<-ctx.Done()
		return sdk.NothingRestored() // cancelled: whatever it says is not believed
	}
	r := newOwnRig(t, recovering("journaltest", &ev, hang))
	r.killSteps("c1")
	if err := r.store.Save("journaltest", "c1", []byte("e1")); err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	exec, s, _ := r.start(80 * time.Millisecond)
	if deadline.IsZero() || deadline.Sub(began) > time.Second {
		t.Errorf("the context has deadline %v", deadline.Sub(began))
	}
	res := pending(t, exec, "c1")
	if !strings.Contains(res.GetMessage(), "CLEANUP_FAILED") || !strings.Contains(res.GetMessage(), "did not finish within 80ms") {
		t.Errorf("message = %q", res.GetMessage())
	}
	if got := r.entryOf("c1"); got != "e1" {
		t.Errorf("entry = %q", got)
	}
	assertAcceptsSteps(t, r, exec, s)
}

func TestARecoveryThatIgnoresItsContextDoesNotHoldTheAgentEither(t *testing.T) {
	var ev events
	release := make(chan struct{})
	defer close(release)
	r := newOwnRig(t, recovering("journaltest", &ev, func(context.Context, sdk.RecoveryHost, string, []byte) sdk.Recovery {
		<-release
		return sdk.NothingRestored()
	}))
	r.killSteps("c1")
	if err := r.store.Save("journaltest", "c1", []byte("e1")); err != nil {
		t.Fatal(err)
	}
	exec, _, _ := r.start(20 * time.Millisecond)
	if res := pending(t, exec, "c1"); !strings.Contains(res.GetMessage(), "CLEANUP_FAILED") {
		t.Errorf("message = %q", res.GetMessage())
	}
	if !r.hasEntry("c1") {
		t.Error("the entry went")
	}
}

func TestARecoveryThatSavesAfterTheHostGaveUpLeavesTheEntryAsItWas(t *testing.T) {
	var ev events
	release := make(chan struct{})
	saved := make(chan error, 1)
	late := func(_ context.Context, h sdk.RecoveryHost, _ string, _ []byte) sdk.Recovery {
		<-release
		saved <- h.Journal().Save([]byte("late"))
		return sdk.NothingRestored()
	}
	r := newOwnRig(t, recovering("journaltest", &ev, late))
	r.killSteps("c1")
	if err := r.store.Save("journaltest", "c1", []byte("e1")); err != nil {
		t.Fatal(err)
	}
	r.start(20 * time.Millisecond)
	close(release)
	if err := <-saved; err == nil {
		t.Error("the late Save succeeded")
	}
	if got := r.entryOf("c1"); got != "e1" {
		t.Errorf("entry = %q", got)
	}
}

func TestARestoreThatSavesAfterTheStepEndedCannotWriteTheEntry(t *testing.T) {
	release := make(chan struct{})
	saved := make(chan error, 1)
	p := &journaltest.Plugin{RestoreFn: func(_ context.Context, h sdk.RestoreHost, _ sdk.Config, _ sdk.RestoreRequest) (sdk.RestoreResult, error) {
		j := h.Journal()
		go func() {
			<-release
			saved <- j.Save([]byte("late"))
		}()
		return sdk.RestoreResult{Database: "x1"}, nil
	}}
	r := newOwnRig(t, p)
	_, s := r.executorRun("c1")
	if res := s.result(t); res.GetStatus() != agentv1.StepStatus_STEP_STATUS_SUCCEEDED {
		t.Fatalf("result = %v", res)
	}
	close(release)
	if err := <-saved; err == nil {
		t.Error("the late Save succeeded")
	}
	waitFor(t, func() bool { return !r.hasEntry("c1") })
}

func TestThePluginsGetFiveMinutesToRecover(t *testing.T) {
	if pluginhost.RecoveryTimeout != 5*time.Minute {
		t.Errorf("RecoveryTimeout = %v", pluginhost.RecoveryTimeout)
	}
}

func TestARecoveryThatPanicsIsACleanupFailureAndTheEntryStays(t *testing.T) {
	var ev events
	r := newOwnRig(t, recovering("journaltest", &ev, func(context.Context, sdk.RecoveryHost, string, []byte) sdk.Recovery { panic("it broke") }))
	r.killSteps("c1")
	if err := r.store.Save("journaltest", "c1", []byte("e1")); err != nil {
		t.Fatal(err)
	}
	exec, s, _ := r.start(0)
	if res := pending(t, exec, "c1"); !strings.Contains(res.GetMessage(), "CLEANUP_FAILED") || !strings.Contains(res.GetMessage(), "panicked") {
		t.Errorf("message = %q", res.GetMessage())
	}
	if !r.hasEntry("c1") {
		t.Error("the entry went")
	}
	assertAcceptsSteps(t, r, exec, s)
}

// A plugin that keeps no recovery cannot undo its entry: the entry is named and stays.
func TestAnEntryOfAPluginThatCannotRecoverIsNamedAndStays(t *testing.T) {
	r := newOwnRig(t, plainPlugin{&journaltest.Plugin{}})
	r.killSteps("c1")
	if err := r.store.Save("journaltest", "c1", []byte("e1")); err != nil {
		t.Fatal(err)
	}
	exec, _, _ := r.start(0)
	assertCleanupFailedNaming(t, pending(t, exec, "c1"), r.entryPath("c1"))
	if !r.hasEntry("c1") {
		t.Error("the entry went")
	}
}

// plainPlugin hides the recovery of the plugin it wraps.
type plainPlugin struct{ p *journaltest.Plugin }

func (w plainPlugin) Name() string         { return w.p.Name() }
func (w plainPlugin) Version() string      { return w.p.Version() }
func (w plainPlugin) ConfigSchema() []byte { return w.p.ConfigSchema() }
func (w plainPlugin) Prepare(ctx context.Context, h sdk.Host, cfg sdk.Config) error {
	return w.p.Prepare(ctx, h, cfg)
}
func (w plainPlugin) Dump(ctx context.Context, h sdk.Host, cfg sdk.Config) (sdk.Dump, error) {
	return w.p.Dump(ctx, h, cfg)
}

func (w plainPlugin) Stream(ctx context.Context, h sdk.Host, cfg sdk.Config, d sdk.Dump, out io.Writer) error {
	return w.p.Stream(ctx, h, cfg, d, out)
}

// executorRun runs the step of the rig through an executor that drops the entry as the agent does.
func (r *ownRig) executorRun(id string) (*executor.Executor, *sink) {
	r.t.Helper()
	exec, s, _ := r.start(0)
	step := r.step()
	step.CommandId = id
	exec.Submit(step)
	return exec, s
}

// recordsResult wraps the Journaled hook to see the entry before and after.
type seen struct{ before, after bool }

// Scenario: После успешного шага запись удаляется только после записи результата.
func TestAfterASuccessfulStepTheEntryIsRemovedOnlyAfterTheResultIsRecorded(t *testing.T) {
	assertEntryRemovedAfterTheResult(t, func(ctx context.Context, h sdk.RestoreHost, _ sdk.Config, _ sdk.RestoreRequest) (sdk.RestoreResult, error) {
		return sdk.RestoreResult{Database: "x1"}, h.Journal().Save([]byte("e1"))
	}, agentv1.StepStatus_STEP_STATUS_SUCCEEDED)
}

// Scenario: После неудачного шага запись удаляется после записи результата.
func TestAfterAFailedStepTheEntryIsRemovedAfterTheResultIsRecorded(t *testing.T) {
	assertEntryRemovedAfterTheResult(t, func(ctx context.Context, h sdk.RestoreHost, _ sdk.Config, _ sdk.RestoreRequest) (sdk.RestoreResult, error) {
		_ = h.Journal().Save([]byte("e1"))
		return sdk.RestoreResult{}, &sdk.RestoreError{Reason: "TEST_REFUSED", Stage: "check", Detail: "no luck"}
	}, agentv1.StepStatus_STEP_STATUS_FAILED)
}

func assertEntryRemovedAfterTheResult(t *testing.T, restore func(context.Context, sdk.RestoreHost, sdk.Config, sdk.RestoreRequest) (sdk.RestoreResult, error), status agentv1.StepStatus) {
	t.Helper()
	r := newOwnRig(t, &journaltest.Plugin{RestoreFn: restore})
	s := newSink()
	var at seen
	exec, err := executor.New(executor.Options{
		Role: role.Source, Handlers: r.handlers, Sink: s, StateDir: r.state, Repositories: []string{"main"},
		Journaled: func(res *agentv1.StepResult) {
			at.before = r.hasEntry("c1")
			r.journal.Journaled(res)
			at.after = r.hasEntry("c1")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Close(context.Background()) })
	exec.Submit(r.step())
	if res := s.result(t); res.GetStatus() != status {
		t.Fatalf("result = %v", res)
	}
	waitFor(t, func() bool { return !r.hasEntry("c1") })
	if !at.before || at.after {
		t.Errorf("entry present when the result was recorded: %v, after the host let go: %v", at.before, at.after)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("the condition did not come")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Scenario: Запись, которую плагин просил сохранить, остаётся до следующего старта.
func TestAnEntryThePluginAskedToKeepStaysUntilTheNextStart(t *testing.T) {
	var ev events
	p := &journaltest.Plugin{
		RestoreFn: func(_ context.Context, h sdk.RestoreHost, _ sdk.Config, _ sdk.RestoreRequest) (sdk.RestoreResult, error) {
			_ = h.Journal().Save([]byte("e1"))
			return sdk.RestoreResult{}, &sdk.RestoreError{Reason: "TEST_REFUSED", Stage: "restore", Detail: "no luck", Leftover: []string{"thing a"}, Cleanup: "boom", Retain: true}
		},
		RecoverFn: func(_ context.Context, _ sdk.RecoveryHost, id string, entry []byte) sdk.Recovery {
			ev.add("recover %s %s", id, entry)
			return sdk.NothingRestored()
		},
	}
	r := newOwnRig(t, p)
	exec, s := r.executorRun("c1")
	if res := s.result(t); res.GetStatus() != agentv1.StepStatus_STEP_STATUS_FAILED {
		t.Fatalf("result = %v", res)
	}
	_ = exec
	time.Sleep(50 * time.Millisecond) // the host has had its time to remove it, if it were going to
	if got := r.entryOf("c1"); got != "e1" {
		t.Fatalf("entry = %q", got)
	}
	r.rebuild()
	r.recoverAll(0)
	if got := ev.all(); !slices.Equal(got, []string{"recover c1 e1"}) {
		t.Errorf("the next start recovered %q", got)
	}
}

// Scenario: Запись остаётся, если результат записать не удалось.
func TestTheEntryStaysIfTheResultCouldNotBeRecorded(t *testing.T) {
	var ev events
	r := newOwnRig(t, recovering("journaltest", &ev, nil))
	r.killSteps("c1")
	if err := r.store.Save("journaltest", "c1", []byte("e1")); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("c1"))
	blocker := filepath.Join(r.state, "results", hex.EncodeToString(sum[:])+".json")
	if err := os.MkdirAll(filepath.Join(blocker, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	rec := r.recoverAll(0)
	journaled := 0
	exec, err := executor.New(executor.Options{
		Role: role.Source, Handlers: r.handlers, Sink: newSink(), StateDir: r.state, Repositories: []string{"main"},
		Interrupted: rec.Interrupted, Journaled: func(res *agentv1.StepResult) { journaled++; r.journal.Journaled(res) },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Close(context.Background()) })
	rec.ExecutorStarted()
	if res := pending(t, exec, "c1"); res.GetStatus() != agentv1.StepStatus_STEP_STATUS_FAILED {
		t.Errorf("result = %v", res)
	}
	if journaled != 0 {
		t.Fatalf("the result of c1 was recorded after all (%d): the test does not fail the executor", journaled)
	}
	if !r.hasEntry("c1") {
		t.Error("the entry went though the result is not on disk")
	}
}

// Scenario: Запись команды с уже записанным результатом удаляется после удачного повторного разбора.
func TestAnEntryOfACommandWithARecordedResultIsRemovedAfterASuccessfulRetry(t *testing.T) {
	var ev events
	p := &journaltest.Plugin{
		RestoreFn: func(_ context.Context, h sdk.RestoreHost, _ sdk.Config, _ sdk.RestoreRequest) (sdk.RestoreResult, error) {
			_ = h.Journal().Save([]byte("e1"))
			return sdk.RestoreResult{}, &sdk.RestoreError{Reason: "TEST_REFUSED", Stage: "restore", Leftover: []string{"thing a"}, Retain: true}
		},
		RecoverFn: func(_ context.Context, _ sdk.RecoveryHost, id string, entry []byte) sdk.Recovery {
			ev.add("recover %s %s", id, entry)
			return sdk.NothingRestored()
		},
	}
	r := newOwnRig(t, p)
	_, s := r.executorRun("c1")
	first := s.result(t)
	waitFor(t, func() bool { return len(r.journalFiles()) == 1 })
	r.rebuild()
	asked := 0
	rec := r.recoverAll(0)
	exec, err := executor.New(executor.Options{
		Role: role.Source, Handlers: r.handlers, Sink: newSink(), StateDir: r.state, Repositories: []string{"main"},
		Interrupted: func(id string, a agentv1.Action) *executor.Interruption { asked++; return rec.Interrupted(id, a) },
		Journaled:   r.journal.Journaled,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Close(context.Background()) })
	if r.hasEntry("c1") != true {
		t.Fatal("the entry went before the executor started")
	}
	rec.ExecutorStarted()
	if r.hasEntry("c1") || asked != 0 {
		t.Errorf("entry present: %v, executor asked %d times", r.hasEntry("c1"), asked)
	}
	if got := pending(t, exec, "c1"); !proto.Equal(got, first) {
		t.Errorf("the result changed: %v, was %v", got, first)
	}
}

func (r *ownRig) journalFiles() []string {
	entries, _ := os.ReadDir(r.journalDir())
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// Scenario: Запись завершённого восстановления неизвестной исполнителю команды удаляется после его старта.
func TestAnEntryOfACompletedRestoreOfACommandTheExecutorDoesNotKnowIsRemovedAfterItStarts(t *testing.T) {
	var ev events
	r := newOwnRig(t, recovering("journaltest", &ev, outcomeOf(sdk.Restored(sdk.RestoreResult{Database: "x1"}))))
	if err := r.store.Save("journaltest", "c9", []byte("e9")); err != nil {
		t.Fatal(err)
	}
	rec := r.recoverAll(0)
	if !r.hasEntry("c9") {
		t.Fatal("the entry went before the executor started")
	}
	exec, err := executor.New(executor.Options{Role: role.Source, Handlers: r.handlers, Sink: newSink(), StateDir: r.state, Repositories: []string{"main"}, Interrupted: rec.Interrupted, Journaled: r.journal.Journaled})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Close(context.Background()) })
	rec.ExecutorStarted()
	if r.hasEntry("c9") {
		t.Error("the entry stays")
	}
}

// An entry the recovery asked to keep stays after the executor started too.
func TestAnEntryTheRecoveryAskedToKeepOfAnUnknownCommandStays(t *testing.T) {
	var ev events
	r := newOwnRig(t, recovering("journaltest", &ev, outcomeOf(sdk.LeftBehind([]string{"thing a"}, "boom", true))))
	if err := r.store.Save("journaltest", "c9", []byte("e9")); err != nil {
		t.Fatal(err)
	}
	r.start(0)
	if !r.hasEntry("c9") {
		t.Error("the entry went")
	}
}

// A corrupt entry and the entry of an unknown plugin are never removed by the host.
func TestTheHostNeverRemovesWhatItCouldNotRead(t *testing.T) {
	r := newOwnRig(t, &journaltest.Plugin{})
	if err := r.store.Save("gone", "c9", []byte("e9")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.entryPath("c8"), []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.start(0)
	if !r.hasEntry("c9") {
		t.Error("the entry of the unknown plugin went")
	}
	if _, err := os.Stat(r.entryPath("c8")); err != nil {
		t.Errorf("the corrupt entry went: %v", err)
	}
}

// The host removes entries of commands it knows about only.
func TestTheHostDoesNotRemoveTheEntryOfACommandItNeverSaw(t *testing.T) {
	r := newOwnRig(t, &journaltest.Plugin{})
	if err := r.store.Save("journaltest", "c5", []byte("e5")); err != nil {
		t.Fatal(err)
	}
	r.journal.Journaled(&agentv1.StepResult{CommandId: "c5", Status: agentv1.StepStatus_STEP_STATUS_SUCCEEDED})
	if !r.hasEntry("c5") {
		t.Error("an entry the host did not know went")
	}
	r.journal.Journaled(&agentv1.StepResult{CommandId: "c6", Status: agentv1.StepStatus_STEP_STATUS_SUCCEEDED})
}
