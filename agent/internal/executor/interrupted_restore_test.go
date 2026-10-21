// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package executor_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/executor"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

func restoreStep() *agentv1.RunStep {
	return &agentv1.RunStep{CommandId: "c1", Plugin: "files", Action: agentv1.Action_ACTION_RESTORE, RepositoryName: "main"}
}

// R2a (ВК14). Scenario: Прерванное восстановление сообщает о провале и об откате.
func TestARestoreInterruptedByARestartIsWordedByTheOwnerOfRestores(t *testing.T) {
	var asked []string
	f := setup(t, func(o *executor.Options) {
		o.Interrupted = func(id string, action agentv1.Action) *executor.Interruption {
			asked = append(asked, id+" "+action.String())
			if action != agentv1.Action_ACTION_RESTORE {
				return nil
			}
			return &executor.Interruption{Message: interrupted + "; nothing was restored"}
		}
	})
	f.e.Submit(restoreStep())
	accepted(t, f.sink, "c1")
	f.files.next(t)
	f.e.Submit(backup("c2"))
	accepted(t, f.sink, "c2")
	f.restart(t)
	wantResult(t, pendingResult(t, f.e, "c1"), "c1", failed, interrupted+"; nothing was restored")
	wantResult(t, pendingResult(t, f.e, "c2"), "c2", failed, interrupted)
	if got := pendingResult(t, f.e, "c1").GetRestore(); got != nil {
		t.Errorf("output = %v, want none", got)
	}
}

// Scenario: Неудачная уборка при старте называется в результате прерванного шага.
func TestAnInterruptedRestoreWhoseRollbackFailedCarriesWhatIsLeft(t *testing.T) {
	f := setup(t, func(o *executor.Options) {
		o.Interrupted = func(id string, action agentv1.Action) *executor.Interruption {
			return &executor.Interruption{
				Message: "CLEANUP_FAILED (rollback): " + interrupted,
				Output:  &agentv1.RestoreOutput{Leftover: "/r/.sard-staging/" + id},
			}
		}
	})
	f.e.Submit(restoreStep())
	accepted(t, f.sink, "c1")
	f.files.next(t)
	f.restart(t)
	r := pendingResult(t, f.e, "c1")
	wantResult(t, r, "c1", failed, "CLEANUP_FAILED (rollback): "+interrupted)
	if r.GetRestore().GetLeftover() != "/r/.sard-staging/c1" {
		t.Errorf("output = %v", r.GetOutput())
	}
	// The stored result is the same after another restart.
	f.restart(t)
	if got := pendingResult(t, f.e, "c1").GetRestore().GetLeftover(); got != "/r/.sard-staging/c1" {
		t.Errorf("leftover after a second restart = %q", got)
	}
}

func TestWithoutAnOwnerOfRestoresAnInterruptedRestoreHasTheD13Message(t *testing.T) {
	f := setup(t, nil)
	f.e.Submit(restoreStep())
	accepted(t, f.sink, "c1")
	f.files.next(t)
	f.restart(t)
	wantResult(t, pendingResult(t, f.e, "c1"), "c1", failed, interrupted)
}

// Scenario: Убитое после переноса восстановление сообщает об успехе с итогом из метки (ВК14, случай 2).
func TestARestoreInterruptedAfterItsMoveIsReportedAsSucceeded(t *testing.T) {
	f := setup(t, func(o *executor.Options) {
		o.Interrupted = func(string, agentv1.Action) *executor.Interruption {
			return &executor.Interruption{
				Succeeded: true,
				Message:   "agent restarted after the restore completed",
				Output:    &agentv1.RestoreOutput{Target: "/r/s1", FilesRestored: 2, BytesRestored: 4},
			}
		}
	})
	f.e.Submit(restoreStep())
	accepted(t, f.sink, "c1")
	f.files.next(t)
	f.restart(t)
	r := pendingResult(t, f.e, "c1")
	wantResult(t, r, "c1", succeeded, "agent restarted after the restore completed")
	if got := r.GetRestore(); got.GetTarget() != "/r/s1" || got.GetFilesRestored() != 2 || got.GetBytesRestored() != 4 {
		t.Errorf("output = %v", got)
	}
}

// Scenario: Каталог с меткой удаляется только после записи результата.
func TestTheJournaledHookRunsAfterTheResultIsSaved(t *testing.T) {
	var journaled []string
	f := setup(t, func(o *executor.Options) {
		o.Journaled = func(r *agentv1.StepResult) {
			if _, err := os.Stat(fileOf(&fixture{opts: *o}, "results", r.GetCommandId())); err != nil {
				t.Errorf("the result of %s is not on disk when the hook runs: %v", r.GetCommandId(), err)
			}
			journaled = append(journaled, r.GetCommandId()+" "+r.GetStatus().String())
		}
	})
	wantResult(t, runOnce(t, f, "c1"), "c1", succeeded, "")
	if len(journaled) != 1 || journaled[0] != "c1 STEP_STATUS_SUCCEEDED" {
		t.Errorf("journaled = %v", journaled)
	}
}

func TestTheJournaledHookIsNotToldOfAResultThatCouldNotBeSaved(t *testing.T) {
	var journaled []string
	f := setup(t, func(o *executor.Options) {
		o.Journaled = func(r *agentv1.StepResult) { journaled = append(journaled, r.GetCommandId()) }
	})
	results := filepath.Join(f.opts.StateDir, "results")
	if err := os.RemoveAll(results); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(results, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	wantResult(t, runOnce(t, f, "c1"), "c1", succeeded, "")
	if len(journaled) != 0 {
		t.Errorf("journaled = %v", journaled)
	}
}

func TestTheJournaledHookIsToldOfAnInterruptedStepOnceItsResultIsSaved(t *testing.T) {
	var journaled []string
	f := setup(t, func(o *executor.Options) {
		o.Journaled = func(r *agentv1.StepResult) { journaled = append(journaled, r.GetCommandId()) }
	})
	f.e.Submit(restoreStep())
	accepted(t, f.sink, "c1")
	f.files.next(t)
	f.restart(t)
	if len(journaled) != 1 || journaled[0] != "c1" {
		t.Errorf("journaled = %v", journaled)
	}
}
