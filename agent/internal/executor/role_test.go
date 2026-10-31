// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package executor_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/role"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// docs/specs/agent/agent-role.feature, tag @executor.

const roleMismatch = agentv1.RejectReason_REJECT_REASON_ROLE_MISMATCH

// withRole runs the executor as an agent of that role with a plugin "qa"
// that announces every action, and the repository "qa".
func withRole(t *testing.T, role role.Role, tune ...func(*executor.Options)) (*fixture, *fakeHandler) {
	t.Helper()
	qa := newHandler(false)
	qa.actions = []agentv1.Action{agentv1.Action_ACTION_BACKUP, agentv1.Action_ACTION_RESTORE, agentv1.Action_ACTION_RUN, agentv1.Action_ACTION_VERIFY}
	f := setup(t, func(o *executor.Options) {
		o.Role = role
		o.Repositories = []string{"qa"}
		o.Handlers = registry{"qa": qa, "files": newHandler(false)}
		for _, f := range tune {
			f(o)
		}
	})
	return f, qa
}

func step(id, plugin string, action agentv1.Action) *agentv1.RunStep {
	return &agentv1.RunStep{CommandId: id, Plugin: plugin, Action: action, RepositoryName: "qa"}
}

// wantRoleMismatch checks the "step rejected by role" outcome: the result,
// no handler call and nothing journaled.
func wantRoleMismatch(t *testing.T, f *fixture, h *fakeHandler, id string, role role.Role, action agentv1.Action) *agentv1.StepResult {
	t.Helper()
	r := f.sink.result(t)
	if r.GetCommandId() != id || r.GetStatus() != rejected || r.GetRejectReason() != roleMismatch {
		t.Fatalf("result = %v", r)
	}
	if !strings.Contains(r.GetMessage(), string(role)) || !strings.Contains(r.GetMessage(), action.String()) {
		t.Errorf("message = %q", r.GetMessage())
	}
	h.idle(t)
	if exists(fileOf(f, "journal", id)) {
		t.Error("the rejected step was journaled")
	}
	return r
}

// Scenario: Верификатор отклоняет бэкап, восстановление и скрипт
func TestAVerifierRejectsBackupRestoreAndRun(t *testing.T) {
	for _, action := range []agentv1.Action{agentv1.Action_ACTION_BACKUP, agentv1.Action_ACTION_RESTORE, agentv1.Action_ACTION_RUN} {
		f, qa := withRole(t, role.Verifier)
		f.e.Submit(step("c1", "qa", action))
		wantRoleMismatch(t, f, qa, "c1", role.Verifier, action)
	}
}

// Scenario: Источник отклоняет проверку даже при плагине, который её умеет
func TestASourceRejectsVerifyEvenWhenThePluginCanDoIt(t *testing.T) {
	f, qa := withRole(t, role.Source)
	f.e.Submit(step("c1", "qa", agentv1.Action_ACTION_VERIFY))
	wantRoleMismatch(t, f, qa, "c1", role.Source, agentv1.Action_ACTION_VERIFY)
}

// Scenario: Верификатор пропускает проверку дальше шлюза роли
func TestAVerifierPassesVerifyPastTheRoleGate(t *testing.T) {
	f, qa := withRole(t, role.Verifier)
	f.e.Submit(step("c1", "qa", agentv1.Action_ACTION_VERIFY))
	accepted(t, f.sink, "c1")
	qa.next(t)
	if qa.runs.Load() != 1 {
		t.Errorf("runs = %d", qa.runs.Load())
	}
}

// Scenario: Проверка встроенным плагином на верификаторе отклоняется как неподдерживаемая, а не по роли
func TestAVerifierRejectsVerifyOfABuiltInPluginAsUnsupportedNotByRole(t *testing.T) {
	f, _ := withRole(t, role.Verifier)
	f.e.Submit(step("c1", "files", agentv1.Action_ACTION_VERIFY))
	r := f.sink.result(t)
	if r.GetStatus() != rejected || r.GetRejectReason() != agentv1.RejectReason_REJECT_REASON_UNSPECIFIED || !strings.Contains(r.GetMessage(), "does not support") {
		t.Fatalf("result = %v", r)
	}
}

// Scenario: Источник выполняет бэкап как раньше
func TestASourceRunsABackupAsBefore(t *testing.T) {
	f, qa := withRole(t, role.Source)
	f.e.Submit(step("c1", "qa", agentv1.Action_ACTION_BACKUP))
	accepted(t, f.sink, "c1")
	qa.next(t).finish(snapshot("s1"), nil)
	r := f.sink.result(t)
	// Scenario: Успешный шаг не заполняет reject_reason
	if r.GetStatus() != succeeded || r.GetRejectReason() != agentv1.RejectReason_REJECT_REASON_UNSPECIFIED {
		t.Errorf("result = %v", r)
	}
	if qa.runs.Load() != 1 {
		t.Errorf("runs = %d", qa.runs.Load())
	}
}

// Scenario: Шлюз роли срабатывает раньше проверки плагина
func TestTheRoleGateComesBeforeThePluginAndRepositoryChecks(t *testing.T) {
	f, qa := withRole(t, role.Verifier)
	f.e.Submit(&agentv1.RunStep{CommandId: "c1", Plugin: "postgres", Action: agentv1.Action_ACTION_BACKUP, RepositoryName: "offsite"})
	wantRoleMismatch(t, f, qa, "c1", role.Verifier, agentv1.Action_ACTION_BACKUP)
}

// Scenario: Шлюз роли срабатывает при полной очереди
func TestTheRoleGateWorksWithAFullQueue(t *testing.T) {
	f, qa := withRole(t, role.Verifier, func(o *executor.Options) { o.MaxQueue = 1 })
	for i := 1; i <= 2; i++ {
		f.e.Submit(step(fmt.Sprintf("v%d", i), "qa", agentv1.Action_ACTION_VERIFY))
		accepted(t, f.sink, fmt.Sprintf("v%d", i))
	}
	qa.next(t)
	f.e.Submit(step("v3", "qa", agentv1.Action_ACTION_VERIFY))
	if r := f.sink.result(t); !strings.Contains(r.GetMessage(), "the queue is full") || r.GetRejectReason() != agentv1.RejectReason_REJECT_REASON_UNSPECIFIED {
		t.Fatalf("result = %v", r)
	}
	f.e.Submit(step("c9", "qa", agentv1.Action_ACTION_BACKUP))
	wantRoleMismatch(t, f, qa, "c9", role.Verifier, agentv1.Action_ACTION_BACKUP)
}

// Scenario: Шлюз роли не зависит от записи на диск
func TestTheRoleGateDoesNotDependOnTheDisk(t *testing.T) {
	f, qa := withRole(t, role.Verifier)
	breakJournal(t, f)
	f.e.Submit(step("c1", "qa", agentv1.Action_ACTION_BACKUP))
	r := f.sink.result(t)
	if r.GetRejectReason() != roleMismatch || strings.HasPrefix(r.GetMessage(), "cannot record the command: ") {
		t.Fatalf("result = %v", r)
	}
	qa.idle(t)
}

// Scenario: Прочие отказы шага не помечаются как несовпадение роли
func TestOtherRejectionsAreNotMarkedAsARoleMismatch(t *testing.T) {
	rows := map[string]*agentv1.RunStep{
		"unknown plugin":     {CommandId: "c1", Plugin: "nope", Action: agentv1.Action_ACTION_BACKUP, RepositoryName: "qa"},
		"unknown repository": {CommandId: "c1", Plugin: "qa", Action: agentv1.Action_ACTION_BACKUP, RepositoryName: "nope"},
		"unspecified action": {CommandId: "c1", Plugin: "qa", Action: agentv1.Action_ACTION_UNSPECIFIED, RepositoryName: "qa"},
		"action 99":          {CommandId: "c1", Plugin: "qa", Action: agentv1.Action(99), RepositoryName: "qa"},
	}
	for name, s := range rows {
		f, _ := withRole(t, role.Source)
		f.e.Submit(s)
		r := f.sink.result(t)
		if r.GetStatus() != rejected || r.GetRejectReason() != agentv1.RejectReason_REJECT_REASON_UNSPECIFIED {
			t.Errorf("%s: result = %v", name, r)
		}
	}
}

// Scenario: Повторная отправка шага не своей роли получает тот же отказ без запуска
func TestARepeatedStepOfAnotherRoleGetsTheSameRefusalWithoutRunning(t *testing.T) {
	f, qa := withRole(t, role.Verifier)
	f.e.Submit(step("c1", "qa", agentv1.Action_ACTION_BACKUP))
	first := wantRoleMismatch(t, f, qa, "c1", role.Verifier, agentv1.Action_ACTION_BACKUP)
	f.e.Submit(step("c1", "qa", agentv1.Action_ACTION_BACKUP))
	again := f.sink.result(t)
	if again.GetStatus() != rejected || again.GetRejectReason() != roleMismatch || again.GetMessage() != first.GetMessage() {
		t.Fatalf("again = %v", again)
	}
	if qa.runs.Load() != 0 {
		t.Errorf("runs = %d", qa.runs.Load())
	}
}

func TestNewRequiresARole(t *testing.T) {
	for _, role := range []role.Role{"", "admin"} {
		_, err := executor.New(executor.Options{Handlers: registry{}, Sink: newSink(), StateDir: stateDir(t), Role: role})
		if err == nil || !strings.Contains(err.Error(), "role") {
			t.Errorf("role %q: err = %v", role, err)
		}
	}
}

// docs/specs/agent/retention-forget.feature (L1a), scenario "Верификатор
// отклоняет шаг FORGET шлюзом роли": the key of a verifier only reads (V-D7).
func TestAVerifierRejectsForgetAtTheRoleGate(t *testing.T) {
	forget := newHandler(false)
	forget.actions = []agentv1.Action{agentv1.Action_ACTION_FORGET}
	f, qa := withRole(t, role.Verifier, func(o *executor.Options) { o.Forget = forget })
	f.e.Submit(step("c1", "", agentv1.Action_ACTION_FORGET))
	wantRoleMismatch(t, f, qa, "c1", role.Verifier, agentv1.Action_ACTION_FORGET)
	forget.idle(t)
}

// A FORGET step has no plugin: the executor runs it with its own handler.
func TestASourceRunsForgetWithTheForgetHandlerAndNoPlugin(t *testing.T) {
	forget := newHandler(false)
	forget.actions = []agentv1.Action{agentv1.Action_ACTION_FORGET}
	f, _ := withRole(t, role.Source, func(o *executor.Options) { o.Forget = forget })
	f.e.Submit(step("c1", "", agentv1.Action_ACTION_FORGET))
	accepted(t, f.sink, "c1")
	call := forget.next(t)
	if call.step.GetAction() != agentv1.Action_ACTION_FORGET {
		t.Errorf("step = %v", call.step)
	}
	call.finish(&agentv1.StepResult{Output: &agentv1.StepResult_Forget{Forget: &agentv1.ForgetOutput{LockHolder: "h"}}}, nil)
	r := f.sink.result(t)
	if r.GetStatus() != agentv1.StepStatus_STEP_STATUS_SUCCEEDED || r.GetForget().GetLockHolder() != "h" {
		t.Errorf("result = %v", r)
	}
}

// An agent without the handler (a verifier-only build, an old test host)
// refuses the step before any work.
func TestAnAgentWithoutAForgetHandlerRejectsForget(t *testing.T) {
	f, qa := withRole(t, role.Source)
	f.e.Submit(step("c1", "", agentv1.Action_ACTION_FORGET))
	r := f.sink.result(t)
	if r.GetStatus() != rejected || r.GetRejectReason() != agentv1.RejectReason_REJECT_REASON_UNSPECIFIED || !strings.Contains(r.GetMessage(), "FORGET") {
		t.Errorf("result = %v", r)
	}
	qa.idle(t)
}

// A FORGET step needs a repository like the other data actions.
func TestAForgetStepWithoutARepositoryIsRejected(t *testing.T) {
	forget := newHandler(false)
	forget.actions = []agentv1.Action{agentv1.Action_ACTION_FORGET}
	f, _ := withRole(t, role.Source, func(o *executor.Options) { o.Forget = forget })
	s := step("c1", "", agentv1.Action_ACTION_FORGET)
	s.RepositoryName = ""
	f.e.Submit(s)
	if r := f.sink.result(t); r.GetStatus() != rejected || !strings.Contains(r.GetMessage(), "needs a repository") {
		t.Errorf("result = %v", r)
	}
	forget.idle(t)
}

// The other actions still need a plugin; an action of a number this agent
// does not know is still refused without a reason code.
func TestAnUnknownActionIsStillRejectedWithoutAReasonCode(t *testing.T) {
	f, qa := withRole(t, role.Source)
	f.e.Submit(step("c1", "qa", agentv1.Action(99)))
	r := f.sink.result(t)
	if r.GetStatus() != rejected || r.GetRejectReason() != agentv1.RejectReason_REJECT_REASON_UNSPECIFIED {
		t.Errorf("result = %v", r)
	}
	qa.idle(t)
}
