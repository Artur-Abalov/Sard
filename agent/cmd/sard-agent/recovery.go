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
	// journal and recovered are the restore journal of the plugins that restore
	// themselves (R2c, ADR 0066) and what their plugins made of it at start; nil
	// on a host without one.
	journal   *pluginhost.Journal
	recovered *pluginhost.JournalRecovery
}

// newRecoveryWith settles .sard-staging before any step is accepted; journal and
// recovered belong to a host whose plugins keep entries in the restore journal
// (recovered is what they made of them), nil on a host without one.
func newRecoveryWith(engine *consolerestore.Engine, journal *pluginhost.Journal, recovered *pluginhost.JournalRecovery) *recovery {
	found := engine.CleanStaging()
	return &recovery{
		engine: engine, found: found, worded: pluginhost.Interrupted(found), notified: map[string]bool{},
		journal: journal, recovered: recovered,
	}
}

// interrupted is the Interrupted option of the executor: the outcome the
// plugin made of its journal entry, if the command has one.
func (r *recovery) interrupted(commandID string, action agentv1.Action) *executor.Interruption {
	r.notified[commandID] = true
	if r.recovered != nil {
		if i := r.recovered.Interrupted(commandID, action); i != nil {
			return i
		}
	}
	return r.worded(commandID, action)
}

// journaled is the Journaled option of the executor: a restore that
// succeeded has its result on disk, so its marker is no longer needed. A
// failed result may name what the rollback left behind: that stays. The entry
// of the restore journal goes unless its plugin asked to keep it.
func (r *recovery) journaled(res *agentv1.StepResult) {
	if r.journal != nil {
		r.journal.Journaled(res)
	}
	if res.GetRestore() != nil && res.GetStatus() == agentv1.StepStatus_STEP_STATUS_SUCCEEDED {
		r.engine.Settle(res.GetCommandId())
	}
}

// executorStarted: the executor has judged the journal. A completed restore it
// did not ask about is of a command it does not know, or one whose result is
// stored already: nothing waits for its marker.
func (r *recovery) executorStarted() {
	if r.recovered != nil {
		r.recovered.ExecutorStarted()
	}
	for _, ev := range r.found {
		if ev.Verdict == consolerestore.Completed && !r.notified[ev.Name] {
			r.engine.Settle(ev.Name)
		}
	}
}
