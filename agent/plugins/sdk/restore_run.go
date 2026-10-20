// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Artur Abalov

package sdk

import (
	"context"
	"io"
	"time"
)

// RestoreRunner is implemented by a Restorer that restores a snapshot itself,
// instead of letting the agent write a directory tree from its plan (a
// database, a device). The agent then makes no temporary directory: the plugin
// reads the snapshot through the RestoreHost and does what it needs, all or
// nothing. A plugin without it is restored as a tree by the agent.
//
// PlanRestore still reads the config first, so that an invalid one rejects the
// step before Restore runs; its plan is not used.
type RestoreRunner interface {
	// Restore restores the snapshot req.SnapshotID of the repository of the
	// step. It returns when ctx is done. An error is reported to the server;
	// a *RestoreError sets its reason, stage and what is left behind.
	Restore(ctx context.Context, h RestoreHost, cfg Config, req RestoreRequest) (RestoreResult, error)
}

// RestoreRequest says which snapshot to restore, and for which command.
type RestoreRequest struct {
	// CommandID is the id of the step; the journal entry is the entry of it.
	CommandID string
	// SnapshotID is the full id of the snapshot, 64 lower-case hexadecimal characters.
	SnapshotID string
}

// RestoreResult is what a restore achieved. Fields that do not apply stay zero.
type RestoreResult struct {
	// Target is what was restored into, as the console shows it; a restore of
	// a database may leave it empty, then the name of the database is used.
	Target string
	// Database is the name of the database restored, if the restore is of one.
	Database string
	// Files and Bytes count what a restore of files wrote.
	Files, Bytes uint64
	// Tables and Roles count what a restore of a database created.
	Tables, Roles uint64
}

// Phase is a stage of a restore the plugin reports to the console.
type Phase int

// The phases of a restore, in the order they happen.
const (
	PhasePreparing Phase = iota + 1
	PhaseRestoring
)

// SnapshotInfo is a snapshot of the repository of the step.
type SnapshotInfo struct {
	// ID is the full id, 64 hexadecimal characters.
	ID   string
	Time time.Time
	// Tags are all tags of the snapshot, "key=value", as restic gives them.
	Tags []string
}

// Journal keeps the entry of the command a plugin needs to undo a restore the
// agent was killed in the middle of. The agent writes it durably (a temporary
// file, fsync, rename), whole or not at all, and removes it itself once the
// result of the command is recorded. A plugin can neither read, list nor
// remove entries.
type Journal interface {
	// Save replaces the entry of this command. It fails when the entry is
	// larger than 1 MiB, when the plugin already has 64 entries of other
	// commands, when the entry holds the value of a secret the plugin read,
	// and when the disk fails; the previous entry then stays. An entry names
	// secrets, it never holds their values.
	Save(entry []byte) error
}

// RestoreHost is what the agent offers a plugin while it restores. It is safe
// for concurrent use.
type RestoreHost interface {
	Host
	// Phase reports the phase the restore is in; progress goes to it.
	Phase(p Phase)
	// Snapshots lists the snapshots of the repository of the step. A failure
	// of the repository is a *RestoreError: return it as it is, with the stage
	// set (AtStage).
	Snapshots(ctx context.Context) ([]SnapshotInfo, error)
	// Dump writes one file of a snapshot to w as the bytes come, and nothing
	// to disk (restic dump, run as the service user). It stops restic when ctx
	// is done or w fails; a failure of w is then returned, recognised with
	// errors.Is. Other failures are a *RestoreError, as for Snapshots.
	Dump(ctx context.Context, snapshotID, path string, w io.Writer) error
	// Journal is the journal of this command.
	Journal() Journal
}

// Recoverer is implemented by a plugin that keeps journal entries. At every
// start of the agent, before it accepts a step and without a connection to the
// server, the agent calls Recover for each entry of the plugin.
type Recoverer interface {
	// Recover undoes what the restore of the command did before the agent
	// stopped, by the entry, and says how it ended. It returns when ctx is
	// done (the agent gives it 5 minutes).
	Recover(ctx context.Context, h RecoveryHost, commandID string, entry []byte) Recovery
}

// RecoveryHost is what the agent offers a plugin while it recovers.
type RecoveryHost interface {
	// Secret returns the value of the named secret, as Host.Secret does.
	Secret(name string) ([]byte, error)
	// Log sends a line to the log of the agent.
	Log(level Level, text string)
	// Journal rewrites the entry that is being recovered.
	Journal() Journal
}

// RecoveryKind is how a restore that was cut short ended.
type RecoveryKind int

// The kinds of Recovery. The zero value is that nothing was restored.
const (
	RecoveryNothingRestored RecoveryKind = iota
	RecoveryCompleted
	RecoveryLeftBehind
)

// Recovery is the outcome of the recovery of one entry. It becomes the result
// of the command whose restore was cut short.
type Recovery struct {
	Kind RecoveryKind
	// Result is what the restore achieved, for RecoveryCompleted.
	Result RestoreResult
	// Leftover names what could not be undone and Cleanup why, for RecoveryLeftBehind.
	Leftover []string
	Cleanup  string
	// Retain keeps the entry after the result is recorded, to try again at the
	// next start.
	Retain bool
}

// Restored is a restore that completed before the agent stopped.
func Restored(r RestoreResult) Recovery { return Recovery{Kind: RecoveryCompleted, Result: r} }

// NothingRestored is a restore whose effects were all undone.
func NothingRestored() Recovery { return Recovery{Kind: RecoveryNothingRestored} }

// LeftBehind is a restore some of whose effects could not be undone.
func LeftBehind(leftover []string, cleanup string, retain bool) Recovery {
	return Recovery{Kind: RecoveryLeftBehind, Leftover: leftover, Cleanup: cleanup, Retain: retain}
}
