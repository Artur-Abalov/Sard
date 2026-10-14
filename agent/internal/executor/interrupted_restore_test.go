// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package executor_test

import (
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/executor"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

func restoreStep(id string) *agentv1.RunStep {
	return &agentv1.RunStep{CommandId: id, Plugin: "files", Action: agentv1.Action_ACTION_RESTORE, RepositoryName: "main"}
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
	f.e.Submit(restoreStep("c1"))
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
	f.e.Submit(restoreStep("c1"))
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
	f.e.Submit(restoreStep("c1"))
	accepted(t, f.sink, "c1")
	f.files.next(t)
	f.restart(t)
	wantResult(t, pendingResult(t, f.e, "c1"), "c1", failed, interrupted)
}
