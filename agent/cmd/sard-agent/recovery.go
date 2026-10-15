// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"github.com/Artur-Abalov/sard/agent/internal/consolerestore"
	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// recovery ties what the start found in the restore directory to the executor
// (ВК14): the evidence words the interrupted restores, and the directory of a
// restore that completed goes only after the executor journaled its result.
type recovery struct {
	engine   *consolerestore.Engine
	found    []consolerestore.Evidence
	worded   func(string, agentv1.Action) *executor.Interruption
	notified map[string]bool // commands the executor asked about
}

// newRecovery settles .sard-staging, before any step is accepted.
func newRecovery(engine *consolerestore.Engine) *recovery {
	found := engine.CleanStaging()
	return &recovery{engine: engine, found: found, worded: pluginhost.Interrupted(found), notified: map[string]bool{}}
}

// interrupted is the Interrupted option of the executor.
func (r *recovery) interrupted(commandID string, action agentv1.Action) *executor.Interruption {
	r.notified[commandID] = true
	return r.worded(commandID, action)
}

// journaled is the Journaled option of the executor: a restore that
// succeeded has its result on disk, so its marker is no longer needed. A
// failed result may name what the rollback left behind: that stays.
func (r *recovery) journaled(res *agentv1.StepResult) {
	if res.GetStatus() == agentv1.StepStatus_STEP_STATUS_SUCCEEDED && res.GetRestore() != nil {
		r.engine.Settle(res.GetCommandId())
	}
}

// executorStarted: the executor has judged the journal. A completed restore it
// did not ask about is of a command it does not know, or one whose result is
// stored already: nothing waits for its marker.
func (r *recovery) executorStarted() {
	for _, ev := range r.found {
		if ev.Verdict == consolerestore.Completed && !r.notified[ev.Name] {
			r.engine.Settle(ev.Name)
		}
	}
}
