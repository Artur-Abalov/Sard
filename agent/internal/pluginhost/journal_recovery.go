// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// RecoveryTimeout is how long a plugin has to recover one entry of the journal.
const RecoveryTimeout = 5 * time.Minute

// recoveryGrace is how long the host waits for a plugin that ignores the end of
// its context before it gives up on it.
const recoveryGrace = 10 * time.Second

// RecoveryOptions configure RecoverJournal.
type RecoveryOptions struct {
	Journal *Journal
	Plugins *sdk.Registry
	Secrets *Secrets
	Logger  *slog.Logger
	// Timeout and Grace default to RecoveryTimeout and 10 seconds.
	Timeout, Grace time.Duration
}

// JournalRecovery is what the start found in the restore journal and the
// plugins made of it.
type JournalRecovery struct {
	journal *Journal

	mu       sync.Mutex
	outcomes map[string]*executor.Interruption
	// adopted are the commands whose entry the host removes once it is settled.
	adopted  []string
	notified map[string]bool
}

// RecoverJournal goes through the journal before any step is accepted and
// without the server: each entry is given to the plugin it belongs to, which
// undoes what the restore did and says how it ended. The outcome becomes the
// result of the interrupted command (Interrupted). An entry that nobody can
// judge stays as it is and is named.
func RecoverJournal(o RecoveryOptions) *JournalRecovery {
	o = o.withDefaults()
	rec := &JournalRecovery{journal: o.Journal, outcomes: map[string]*executor.Interruption{}, notified: map[string]bool{}}
	records, unreadable, err := o.Journal.store.List()
	if err != nil {
		o.Logger.Error("cannot read the restore journal; judge the restores by hand", "error", err)
		return rec
	}
	for _, u := range unreadable {
		o.Logger.Error("cannot read an entry of the restore journal; judge the restore by hand", "command_id", u.CommandID, "path", u.Path, "error", u.Err)
		rec.outcomes[u.CommandID] = leftBehind([]string{u.Path}, u.Err.Error())
	}
	for _, r := range records {
		rec.recoverEntry(o, r.CommandID, r.Plugin, r.Path, r.Entry)
	}
	return rec
}

func (o RecoveryOptions) withDefaults() RecoveryOptions {
	if o.Timeout <= 0 {
		o.Timeout = RecoveryTimeout
	}
	if o.Grace <= 0 {
		o.Grace = recoveryGrace
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	return o
}

// recoverEntry hands one entry to its plugin.
func (rec *JournalRecovery) recoverEntry(o RecoveryOptions, id, pluginName, path string, entry []byte) {
	recoverer := recovererOf(o.Plugins, pluginName)
	if recoverer == nil {
		o.Logger.Error("the restore journal holds an entry of a plugin that cannot recover it; judge the restore by hand",
			"plugin", pluginName, "command_id", id, "path", path)
		rec.outcomes[id] = leftBehind([]string{path}, fmt.Sprintf("this agent has no plugin %q that recovers entries", pluginName))
		return
	}
	host := &recoveryHost{log: o.Logger.With("plugin", pluginName, "command_id", id), secrets: o.Secrets,
		journal: &stepJournal{store: o.Journal, plugin: pluginName, commandID: id}}
	got, err := recoverWithin(o, recoverer, host, id, entry)
	if err != nil {
		o.Logger.Error("the recovery of an entry of the restore journal failed; judge the restore by hand",
			"plugin", pluginName, "command_id", id, "path", path, "error", err)
		rec.outcomes[id] = leftBehind([]string{path}, err.Error())
		return
	}
	o.Journal.adopt(id)
	if got.Retain {
		o.Journal.keep(id)
	}
	rec.adopted = append(rec.adopted, id)
	rec.outcomes[id] = outcomeOf(o.Logger, id, path, got)
}

func recovererOf(reg *sdk.Registry, name string) sdk.Recoverer {
	p, ok := reg.Get(name)
	if !ok {
		return nil
	}
	r, _ := p.(sdk.Recoverer)
	return r
}

// recovered is what the goroutine of a recovery came back with.
type recovered struct {
	got      sdk.Recovery
	panicked any
}

// recoverWithin runs the recovery of a plugin for at most Timeout, and Grace
// more for a plugin that does not heed its context. What a recovery says after
// its time is over is not believed.
func recoverWithin(o RecoveryOptions, r sdk.Recoverer, host sdk.RecoveryHost, id string, entry []byte) (sdk.Recovery, error) {
	ctx, cancel := context.WithTimeout(context.Background(), o.Timeout)
	defer cancel()
	out := awaitRecovery(ctx, startRecovery(ctx, r, host, id, entry), o.Grace)
	switch {
	case out.panicked != nil:
		return sdk.Recovery{}, fmt.Errorf("the plugin panicked: %v", out.panicked)
	case ctx.Err() != nil:
		return sdk.Recovery{}, fmt.Errorf("the recovery did not finish within %s", o.Timeout)
	}
	return out.got, nil
}

// startRecovery runs the recovery in a goroutine of its own; a panic is a value.
func startRecovery(ctx context.Context, r sdk.Recoverer, host sdk.RecoveryHost, id string, entry []byte) <-chan recovered {
	done := make(chan recovered, 1)
	go func() {
		var out recovered
		defer func() {
			out.panicked = recover()
			done <- out
		}()
		out.got = r.Recover(ctx, host, id, entry)
	}()
	return done
}

// awaitRecovery waits for the recovery until its context ends, then for grace more.
func awaitRecovery(ctx context.Context, done <-chan recovered, grace time.Duration) recovered {
	select {
	case out := <-done:
		return out
	case <-ctx.Done():
	}
	select {
	case out := <-done:
		return out
	case <-time.After(grace):
		return recovered{}
	}
}

// outcomeOf words what a recovery came to as the result of the command.
func outcomeOf(log *slog.Logger, id, path string, got sdk.Recovery) *executor.Interruption {
	switch got.Kind {
	case sdk.RecoveryCompleted:
		log.Info("the restore completed before the agent was restarted", "command_id", id)
		return &executor.Interruption{Succeeded: true, Message: "agent restarted after the restore completed", Output: restoreOutput(got.Result)}
	case sdk.RecoveryLeftBehind:
		leftover := got.Leftover
		if len(leftover) == 0 {
			leftover = []string{path}
		}
		log.Error("the recovery of an interrupted restore could not undo everything; remove it by hand",
			"command_id", id, "left", strings.Join(leftover, ", "), "error", got.Cleanup)
		return leftBehind(leftover, got.Cleanup)
	}
	log.Info("recovered an interrupted restore: nothing was restored", "command_id", id)
	return &executor.Interruption{Message: executor.InterruptedMessage + "; nothing was restored"}
}

// leftBehind is CLEANUP_FAILED for an interrupted restore (ВК15).
func leftBehind(leftover []string, cleanup string) *executor.Interruption {
	left := strings.Join(leftover, ", ")
	return &executor.Interruption{
		Message: fmt.Sprintf("%s (%s): %s; left behind: %s (%s); remove it by hand", refusal.CleanupFailed, "rollback", executor.InterruptedMessage, left, cleanup),
		Output:  &agentv1.RestoreOutput{Leftover: left},
	}
}

// Interrupted is the Interrupted option of the executor (for the commands of
// plugins that keep a journal): the outcome of the recovery of the command, or
// nil if the journal had nothing for it.
func (rec *JournalRecovery) Interrupted(commandID string, action agentv1.Action) *executor.Interruption {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	out := rec.outcomes[commandID]
	if out == nil || action != agentv1.Action_ACTION_RESTORE {
		return nil
	}
	rec.notified[commandID] = true
	return out
}

// ExecutorStarted: the executor has judged the journal. The entry of a command
// it did not ask about is of a command it does not know, or one whose result is
// stored already: nothing waits for it, and it goes unless the plugin asked to
// keep it.
func (rec *JournalRecovery) ExecutorStarted() {
	rec.mu.Lock()
	var settle []string
	for _, id := range rec.adopted {
		if !rec.notified[id] {
			settle = append(settle, id)
		}
	}
	rec.mu.Unlock()
	for _, id := range settle {
		rec.journal.settle(id)
	}
}

// recoveryHost is the sdk.RecoveryHost of one entry.
type recoveryHost struct {
	log     *slog.Logger
	secrets *Secrets
	journal *stepJournal
}

var _ sdk.RecoveryHost = (*recoveryHost)(nil)

// Secret implements sdk.RecoveryHost: read from the file of the host; the
// error names the secret, never its file.
func (h *recoveryHost) Secret(name string) ([]byte, error) {
	value, err := h.secrets.Secret(name)
	if err == nil {
		h.journal.noteSecret(name, value)
	}
	return value, err
}

var slogLevels = map[sdk.Level]slog.Level{
	sdk.LevelDebug: slog.LevelDebug, sdk.LevelInfo: slog.LevelInfo, sdk.LevelWarn: slog.LevelWarn, sdk.LevelError: slog.LevelError,
}

// Log implements sdk.RecoveryHost; an unknown level is INFO.
func (h *recoveryHost) Log(level sdk.Level, text string) {
	lvl, ok := slogLevels[level]
	if !ok {
		lvl = slog.LevelInfo
	}
	h.log.Log(context.Background(), lvl, text)
}

// Journal implements sdk.RecoveryHost.
func (h *recoveryHost) Journal() sdk.Journal { return h.journal }
