// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost

import (
	"log/slog"
	"sync"

	"github.com/Artur-Abalov/sard/agent/internal/restorejournal"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// Journal is the host's side of the restore journal (ADR 0066): the plugins
// save the entry of their command through it, and it alone removes entries,
// once the executor has recorded the result of the command. An entry whose
// plugin asked to keep it stays after a failed result, for the next start.
type Journal struct {
	store *restorejournal.Store
	log   *slog.Logger

	mu sync.Mutex
	// known are the commands whose entry the host may remove: saved in this run,
	// or read and handed to its plugin at start. An entry nobody understood is
	// not among them.
	known map[string]bool
	// retained are the known commands whose entry stays after a failure.
	retained map[string]bool
}

// NewJournal returns the journal of the store; a nil log discards.
func NewJournal(store *restorejournal.Store, log *slog.Logger) *Journal {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Journal{store: store, log: log, known: map[string]bool{}, retained: map[string]bool{}}
}

// save writes the entry of the command of the plugin; the host will remove it.
func (j *Journal) save(plugin, commandID string, entry []byte) error {
	if err := j.store.Save(plugin, commandID, entry); err != nil {
		return err
	}
	j.adopt(commandID)
	return nil
}

func (j *Journal) adopt(commandID string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.known[commandID] = true
}

// keep asks for the entry of the command to stay after a failed result.
func (j *Journal) keep(commandID string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.retained[commandID] = true
}

// forget lets go of the command: whether the host may remove its entry now.
func (j *Journal) forget(commandID string) (known, retained bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	known, retained = j.known[commandID], j.retained[commandID]
	delete(j.known, commandID)
	delete(j.retained, commandID)
	return known, retained
}

// Journaled is the Journaled option of the executor: the result is on disk, so
// the entry is no longer needed, unless the plugin asked to keep it and the
// result is not a success.
func (j *Journal) Journaled(res *agentv1.StepResult) {
	id := res.GetCommandId()
	known, retained := j.forget(id)
	if known && (!retained || res.GetStatus() == agentv1.StepStatus_STEP_STATUS_SUCCEEDED) {
		j.remove(id)
	}
}

// settle removes the entry of a command the executor did not ask about after
// the start, unless the plugin asked to keep it.
func (j *Journal) settle(commandID string) {
	if known, retained := j.forget(commandID); known && !retained {
		j.remove(commandID)
	}
}

func (j *Journal) remove(commandID string) {
	if err := j.store.Remove(commandID); err != nil {
		j.log.Error("cannot remove the entry of the restore journal; remove it by hand", "command_id", commandID, "path", j.store.Path(commandID), "error", err)
		return
	}
	j.log.Info("removed the entry of the restore journal", "command_id", commandID)
}
