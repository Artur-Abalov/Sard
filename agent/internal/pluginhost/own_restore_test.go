// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/pluginhost/journaltest"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	filesplugin "github.com/Artur-Abalov/sard/agent/plugins/files"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
	"google.golang.org/protobuf/proto"
)

// refuses is the plugin T that refuses the restore.
func refuses(err error) *journaltest.Plugin {
	return &journaltest.Plugin{RestoreFn: func(context.Context, sdk.RestoreHost, sdk.Config, sdk.RestoreRequest) (sdk.RestoreResult, error) {
		return sdk.RestoreResult{}, err
	}}
}

// recordingFiles is the files plugin that remembers which of its methods were called.
type recordingFiles struct {
	filesplugin.Plugin
	calls *[]string
}

func (p recordingFiles) RestoreConfigSchema() []byte {
	*p.calls = append(*p.calls, "RestoreConfigSchema")
	return p.Plugin.RestoreConfigSchema()
}

func (p recordingFiles) PlanRestore(cfg sdk.Config) (sdk.RestorePlan, error) {
	*p.calls = append(*p.calls, "PlanRestore")
	return p.Plugin.PlanRestore(cfg)
}

func (p recordingFiles) Prepare(ctx context.Context, h sdk.Host, cfg sdk.Config) error {
	*p.calls = append(*p.calls, "Prepare")
	return p.Plugin.Prepare(ctx, h, cfg)
}

// Scenario: Восстановление files выкладывает хост через временный каталог по плану плагина.
func TestAPluginWithoutItsOwnRestoreIsRestoredByTheHostFromItsPlan(t *testing.T) {
	var calls []string
	r := newOwnRig(t, recordingFiles{Plugin: filesplugin.Plugin{AgentVersion: "1.0.0"}, calls: &calls})
	step := r.step()
	step.Plugin, step.ConfigJson = "files", `{"destination": "s1/20261010T030000Z"}`
	step.Tags = map[string]string{"sard.source": "s1"}
	r.repo.restoreTo = restoresOneFile
	res, err := r.run(step)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"RestoreConfigSchema", "PlanRestore"}; !slices.Equal(uniq(calls), want) {
		t.Errorf("the plugin was asked %q, want only %q", calls, want)
	}
	if got := r.repo.restoreReqs[0]; got.Staging == nil || got.Target != "" {
		t.Errorf("restic restore request = %+v: want the held directory of the host, no path", got)
	}
	if _, err := os.Stat(r.dir + "/s1/20261010T030000Z/srv/a"); err != nil {
		t.Error(err)
	}
	if res.GetRestore().GetTarget() != r.dir+"/s1/20261010T030000Z" {
		t.Errorf("output = %v", res.GetRestore())
	}
}

func uniq(s []string) []string { return slices.Compact(slices.Clone(s)) }

// Scenario: Плагин с собственным восстановлением получает шаг, а хост не создаёт временного каталога.
func TestAPluginWithItsOwnRestoreGetsTheStepAndTheHostMakesNoDirectory(t *testing.T) {
	var got []string
	r := newOwnRig(t, &journaltest.Plugin{RestoreFn: func(_ context.Context, _ sdk.RestoreHost, cfg sdk.Config, req sdk.RestoreRequest) (sdk.RestoreResult, error) {
		got = append(got, fmt.Sprintf("%s %s %s %s %s", req.CommandID, req.SnapshotID, cfg, req.Repository, req.Source))
		return sdk.RestoreResult{}, nil
	}})
	step := r.step()
	step.Tags = map[string]string{"sard.source": "s1"}
	if _, err := r.run(step); err != nil {
		t.Fatal(err)
	}
	if want := []string{"c1 " + fullID + " {} main s1"}; !slices.Equal(got, want) {
		t.Errorf("the plugin was called %q, want %q", got, want)
	}
	if _, err := os.Stat(r.dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the restore directory exists: %v", err)
	}
	if len(r.repo.restoreReqs) != 0 {
		t.Error("restic restore ran")
	}
}

// Scenario: Итог плагина становится итогом шага.
func TestTheResultOfThePluginIsTheResultOfTheStep(t *testing.T) {
	r := newOwnRig(t, &journaltest.Plugin{RestoreFn: func(context.Context, sdk.RestoreHost, sdk.Config, sdk.RestoreRequest) (sdk.RestoreResult, error) {
		return sdk.RestoreResult{Database: "x1", Tables: 3, Roles: 1}, nil
	}})
	res, err := r.run(r.step())
	if err != nil {
		t.Fatal(err)
	}
	want := &agentv1.RestoreOutput{Target: "x1", Database: "x1", TablesRestored: 3, RolesRestored: 1}
	if !proto.Equal(res.GetRestore(), want) {
		t.Errorf("output = %v, want %v", res.GetRestore(), want)
	}
}

func TestAResultOfFilesAndATargetIsReportedAsItIs(t *testing.T) {
	r := newOwnRig(t, &journaltest.Plugin{RestoreFn: func(context.Context, sdk.RestoreHost, sdk.Config, sdk.RestoreRequest) (sdk.RestoreResult, error) {
		return sdk.RestoreResult{Target: "/mnt/x", Files: 4, Bytes: 5}, nil
	}})
	res, err := r.run(r.step())
	if err != nil {
		t.Fatal(err)
	}
	want := &agentv1.RestoreOutput{Target: "/mnt/x", FilesRestored: 4, BytesRestored: 5}
	if !proto.Equal(res.GetRestore(), want) {
		t.Errorf("output = %v, want %v", res.GetRestore(), want)
	}
}

// Scenario: Фазы, о которых сообщает плагин, доходят до сервера по порядку.
func TestThePhasesThePluginReportsReachTheServerInOrder(t *testing.T) {
	r := newOwnRig(t, &journaltest.Plugin{RestoreFn: func(_ context.Context, h sdk.RestoreHost, _ sdk.Config, _ sdk.RestoreRequest) (sdk.RestoreResult, error) {
		h.Phase(sdk.PhasePreparing)
		h.Phase(sdk.PhaseRestoring)
		h.Progress(3, 10)
		return sdk.RestoreResult{}, nil
	}})
	_, rep, err := r.runReported(r.step())
	if err != nil {
		t.Fatal(err)
	}
	want := []event{
		{phase: agentv1.StepPhase_STEP_PHASE_PREPARING},
		{phase: agentv1.StepPhase_STEP_PHASE_RESTORING},
		{phase: agentv1.StepPhase_STEP_PHASE_RESTORING, done: 3, total: 10},
	}
	if got := rep.all(); !slices.Equal(got, want) {
		t.Errorf("events = %+v, want %+v", got, want)
	}
}

// Scenario: Конфиг, который отверг план плагина с собственным восстановлением, отклоняет шаг.
func TestAConfigThePlanOfAPluginWithItsOwnRestoreRefusesRejectsTheStep(t *testing.T) {
	called := false
	r := newOwnRig(t, &journaltest.Plugin{
		Plan: func(sdk.Config) (sdk.RestorePlan, error) {
			return sdk.RestorePlan{}, &sdk.ConfigError{Violations: []sdk.Violation{{Path: "/x", Message: "no good"}}}
		},
		RestoreFn: func(context.Context, sdk.RestoreHost, sdk.Config, sdk.RestoreRequest) (sdk.RestoreResult, error) {
			called = true
			return sdk.RestoreResult{}, nil
		},
	})
	_, err := r.run(r.step())
	if !errors.Is(err, executor.ErrRejected) || !strings.Contains(err.Error(), "/x") || called {
		t.Fatalf("err = %v, restore called: %v", err, called)
	}
}

// The plan of a plugin with its own restore says nothing the host uses: no destination is needed.
func TestThePlanOfAPluginWithItsOwnRestoreNeedsNoDestination(t *testing.T) {
	r := newOwnRig(t, &journaltest.Plugin{Plan: func(sdk.Config) (sdk.RestorePlan, error) { return sdk.RestorePlan{}, nil }})
	if _, err := r.run(r.step()); err != nil {
		t.Fatal(err)
	}
}

func TestAConfigOutsideTheSchemaOfAPluginWithItsOwnRestoreRejectsTheStep(t *testing.T) {
	r := newOwnRig(t, &journaltest.Plugin{})
	step := r.step()
	step.ConfigJson = `{"nope": 1}`
	if _, err := r.run(step); !errors.Is(err, executor.ErrRejected) || !errors.Is(err, sdk.ErrInvalidConfig) {
		t.Fatalf("err = %v", err)
	}
}

func TestARestoreOfAPluginWithItsOwnRestoreNeedsAValidSnapshotAndACommandThatNamesAFile(t *testing.T) {
	for name, edit := range map[string]func(*agentv1.RunStep){
		"no snapshot":    func(s *agentv1.RunStep) { s.SnapshotId = "" },
		"short snapshot": func(s *agentv1.RunStep) { s.SnapshotId = "aaaa1111" },
		"command id":     func(s *agentv1.RunStep) { s.CommandId = "../x" },
	} {
		t.Run(name, func(t *testing.T) {
			called := false
			r := newOwnRig(t, &journaltest.Plugin{RestoreFn: func(context.Context, sdk.RestoreHost, sdk.Config, sdk.RestoreRequest) (sdk.RestoreResult, error) {
				called = true
				return sdk.RestoreResult{}, nil
			}})
			step := r.step()
			edit(step)
			if _, err := r.run(step); !errors.Is(err, executor.ErrRejected) || called {
				t.Errorf("err = %v, called: %v", err, called)
			}
		})
	}
}

func TestAnUnknownRepositoryRejectsTheStepOfAPluginWithItsOwnRestore(t *testing.T) {
	r := newOwnRig(t, &journaltest.Plugin{})
	step := r.step()
	step.RepositoryName = "nope"
	if _, err := r.run(step); !errors.Is(err, executor.ErrRejected) {
		t.Fatalf("err = %v", err)
	}
}

// Scenario: Отказ плагина пишется причиной, стадией и текстом.
func TestARefusalOfThePluginIsWordedWithTheReasonTheStageAndTheText(t *testing.T) {
	r := newOwnRig(t, refuses(&sdk.RestoreError{Reason: "TEST_REFUSED", Stage: "check", Detail: "no luck"}))
	res, err := r.run(r.step())
	if err == nil || err.Error() != "TEST_REFUSED (check): no luck; nothing was restored" {
		t.Fatalf("err = %v", err)
	}
	if res != nil {
		t.Errorf("result = %v: a refusal has no output", res)
	}
}

// Scenario: Неудачный откат плагина пишется как CLEANUP_FAILED с тем, что осталось.
func TestARollbackThePluginCouldNotFinishIsWordedAsCleanupFailedWithWhatIsLeft(t *testing.T) {
	r := newOwnRig(t, refuses(&sdk.RestoreError{Reason: "TEST_REFUSED", Stage: "restore", Detail: "no luck", Leftover: []string{"thing a"}, Cleanup: "boom"}))
	res, err := r.run(r.step())
	want := "CLEANUP_FAILED (rollback): TEST_REFUSED (restore): no luck; left behind: thing a (boom); remove it by hand"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %s", err, want)
	}
	if !proto.Equal(res.GetRestore(), &agentv1.RestoreOutput{Leftover: "thing a"}) {
		t.Errorf("output = %v", res.GetRestore())
	}
	if log := r.logs.String(); !strings.Contains(log, "level=ERROR") || !strings.Contains(log, "thing a") {
		t.Errorf("log = %s", log)
	}
}

func TestSeveralThingsLeftBehindAreNamedTogether(t *testing.T) {
	r := newOwnRig(t, refuses(&sdk.RestoreError{Reason: "TEST_REFUSED", Stage: "restore", Detail: "no luck", Leftover: []string{"thing a", "thing b"}, Cleanup: "boom"}))
	res, err := r.run(r.step())
	if err == nil || !strings.Contains(err.Error(), "left behind: thing a, thing b (boom)") || res.GetRestore().GetLeftover() != "thing a, thing b" {
		t.Fatalf("res = %v, err = %v", res, err)
	}
}

// Scenario: Код причины вне образца не уходит на сервер как есть.
func TestAReasonThatIsNoReasonCodeNeverReachesTheServer(t *testing.T) {
	for name, reason := range map[string]string{
		"empty": "", "lower case": "test_refused", "leading digit": "1TEST", "hyphen": "TEST-REFUSED",
		"space": "TEST REFUSED", "new line": "TEST\n", "65 characters": strings.Repeat("A", 65),
	} {
		t.Run(name, func(t *testing.T) {
			r := newOwnRig(t, refuses(&sdk.RestoreError{Reason: reason, Stage: "check", Detail: "no luck"}))
			_, err := r.run(r.step())
			if err == nil || !strings.HasPrefix(err.Error(), "PLUGIN_FAILED (check)") || !strings.Contains(err.Error(), "journaltest") {
				t.Fatalf("err = %v", err)
			}
			if reason != "" && strings.Contains(err.Error(), reason) {
				t.Errorf("the message holds the reason sent: %q", err)
			}
		})
	}
}

// Scenario: Код причины по образцу принимается.
func TestAReasonCodeThatFitsThePatternIsPassedOn(t *testing.T) {
	for name, reason := range map[string]string{"one letter": "A", "digit": "TEST_1", "64 characters": strings.Repeat("A", 64)} {
		t.Run(name, func(t *testing.T) {
			r := newOwnRig(t, refuses(&sdk.RestoreError{Reason: reason, Stage: "check", Detail: "no luck"}))
			_, err := r.run(r.step())
			if err == nil || !strings.HasPrefix(err.Error(), reason+" (check)") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

// Scenario: Стадия, которую задаёт плагин, подходит под образец.
func TestAStageThatIsNoStageNameIsReplacedByTheRestoreStage(t *testing.T) {
	for _, stage := range []string{"Check", "1check", "check it", strings.Repeat("a", 33), "check\n"} {
		r := newOwnRig(t, refuses(&sdk.RestoreError{Reason: "TEST_REFUSED", Stage: stage, Detail: "no luck"}))
		_, err := r.run(r.step())
		if err == nil || !strings.HasPrefix(err.Error(), "PLUGIN_FAILED (restore)") || strings.Contains(err.Error(), stage) {
			t.Errorf("%q: err = %v", stage, err)
		}
	}
}

func TestARefusalWithoutAStageIsWordedAtTheRestoreStage(t *testing.T) {
	r := newOwnRig(t, refuses(&sdk.RestoreError{Reason: "TEST_REFUSED", Detail: "no luck"}))
	if _, err := r.run(r.step()); err == nil || !strings.HasPrefix(err.Error(), "TEST_REFUSED (restore): no luck") {
		t.Fatalf("err = %v", err)
	}
}

func TestARefusalWithAnInvalidReasonStillNamesWhatWasLeft(t *testing.T) {
	r := newOwnRig(t, refuses(&sdk.RestoreError{Reason: "bad", Stage: "check", Leftover: []string{"thing a"}, Cleanup: "boom"}))
	res, err := r.run(r.step())
	if err == nil || !strings.HasPrefix(err.Error(), "CLEANUP_FAILED (rollback): PLUGIN_FAILED (check)") || res.GetRestore().GetLeftover() != "thing a" {
		t.Fatalf("res = %v, err = %v", res, err)
	}
}

// A plugin that fails without a RestoreError is told as its own failure.
func TestAFailureThatIsNoRestoreErrorIsAPluginFailure(t *testing.T) {
	r := newOwnRig(t, refuses(errors.New("it broke")))
	res, err := r.run(r.step())
	if err == nil || err.Error() != "PLUGIN_FAILED (restore): it broke; nothing was restored" || res != nil {
		t.Fatalf("res = %v, err = %v", res, err)
	}
}

// Scenario: Отказ плагина из-за отсутствующей программы кончается подсказкой check.
func TestARefusalBecauseAProgramIsMissingEndsWithTheCheckHint(t *testing.T) {
	r := newOwnRig(t, refuses(&sdk.RestoreError{Reason: "TEST_TOOL_MISSING", Stage: "check", Detail: "no tool", Err: fmt.Errorf("tool %w", sdk.ErrProgramMissing)}))
	_, err := r.run(r.step())
	lines := strings.Split(err.Error(), "\n")
	if err == nil || len(lines) != 2 || lines[1] != "run `sudo sard-agent check` on the host" || !strings.HasSuffix(lines[0], "nothing was restored") {
		t.Fatalf("err = %q", err)
	}
}

// Scenario: Прочий отказ плагина без подсказки check.
func TestAnotherRefusalHasNoCheckHint(t *testing.T) {
	r := newOwnRig(t, refuses(&sdk.RestoreError{Reason: "TEST_REFUSED", Stage: "check", Detail: "no luck"}))
	if _, err := r.run(r.step()); err == nil || strings.Contains(err.Error(), "sard-agent check") {
		t.Fatalf("err = %v", err)
	}
}

// Scenario: Плагин с собственным восстановлением не требует движка консоли на хосте.
func TestAPluginWithItsOwnRestoreNeedsNoRestoreEngineOnTheHost(t *testing.T) {
	var listed []sdk.SnapshotInfo
	p := &journaltest.Plugin{RestoreFn: func(ctx context.Context, h sdk.RestoreHost, _ sdk.Config, _ sdk.RestoreRequest) (sdk.RestoreResult, error) {
		var err error
		listed, err = h.Snapshots(ctx)
		return sdk.RestoreResult{Database: "x1"}, sdk.AtStage(err, "check")
	}}
	r := newOwnRig(t, p)
	r.noEngine = true
	r.rebuild()
	res, err := r.run(r.step())
	if err != nil || res.GetRestore().GetDatabase() != "x1" || len(listed) != 1 {
		t.Fatalf("res %v, err %v, snapshots %v", res, err, listed)
	}
}

func TestTheFailuresOfTheRepositoryAreWordedOnAHostWithoutTheRestoreEngineToo(t *testing.T) {
	var listed []sdk.SnapshotInfo
	r := newOwnRig(t, snapshotsOf(&listed))
	r.noEngine = true
	r.rebuild()
	r.repo.listErr = restic.ErrNetwork
	if _, err := r.run(r.step()); err == nil || !strings.HasPrefix(err.Error(), "BACKEND_UNAVAILABLE (check)") {
		t.Fatalf("err = %v", err)
	}
}
